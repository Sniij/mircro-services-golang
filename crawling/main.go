package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
	"github.com/joho/godotenv"
)

// NewsArticle represents a news article with title and content.
type NewsArticle struct {
	Title   string `json:"title"`
	Content string `json:"content"`
	Date    string `json:"date"`
}

var BASE_URL string
var BASE_URL_DETAIL string

func init() {
	// .env 파일 로드 (로컬 환경에서만 사용)
	if _, isLambda := os.LookupEnv("LAMBDA_TASK_ROOT"); !isLambda {
		if err := godotenv.Load(); err != nil {
			log.Println("No .env file found. Falling back to system environment variables.")
		}
	}
	var err error
	BASE_URL, err = url.QueryUnescape(os.Getenv("BASE_URL"))
	if err != nil {
		log.Printf("failed to get server url: %v", err)
	}
	BASE_URL_DETAIL, err = url.QueryUnescape(os.Getenv("BASE_URL_DETAIL"))
	if err != nil {
		log.Printf("failed to get server url: %v", err)
	}
}

// FetchHTML fetches the HTML document from a given URL.
func FetchHTML(url string) (*goquery.Document, error) {
	req, _ := http.NewRequest("GET", url, nil)
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; v1.0)")

	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch URL: %v", err)
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status code: %d", res.StatusCode)
	}

	doc, err := goquery.NewDocumentFromReader(res.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to parse HTML: %v", err)
	}

	return doc, nil
}

// ScrapeHeadlines extracts the top 5 headline links from the section page.
func ScrapeHeadlines(doc *goquery.Document) ([]string, error) {
	var links []string
	seen := make(map[string]bool) // 중복 제거를 위한 map

	doc.Find("ul.sa_list li a").EachWithBreak(func(i int, s *goquery.Selection) bool {
		if len(links) >= 5 {
			return false
		}

		link, exists := s.Attr("href")
		if exists {
			// 상대 경로 처리
			if link[0] == '/' {
				link = BASE_URL + link
			}

			// 댓글 링크 필터링 및 중복 제거
			if isValidNewsLink(link) && !seen[link] {
				links = append(links, link)
				seen[link] = true // 중복 방지
			}
		}
		return true
	})

	if len(links) == 0 {
		return nil, fmt.Errorf("no headlines found")
	}

	log.Println("Extracted links:", links)

	return links, nil
}
func isValidNewsLink(link string) bool {
	return (len(link) > 0 && strings.Contains(link, BASE_URL_DETAIL) && !strings.Contains(link, "/comment/"))
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && len(substr) > 0 && (len(s)-len(substr) >= 0 && s[len(s)-len(substr):] == substr)
}

// ScrapeArticle extracts the title and content of a news article.
func ScrapeArticle(url string) (NewsArticle, error) {
	doc, err := FetchHTML(url)
	if err != nil {
		return NewsArticle{}, err
	}

	// Extract title
	title := doc.Find(".media_end_head_headline").Text()

	// Remove all <span> tags within #dic_area
	doc.Find("#dic_area span").Remove()

	// Extract content after removing <span> tags
	content := doc.Find("#dic_area").Text()

	// Extract date
	date := doc.Find(".media_end_head_info_datestamp_time").Text()

	if title == "" || content == "" || date == "" {
		return NewsArticle{}, fmt.Errorf("failed to extract title, content, or date")
	}

	return NewsArticle{
		Title:   strings.TrimSpace(title),
		Content: strings.TrimSpace(content),
		Date:    strings.TrimSpace(date),
	}, nil
}

// Handler processes the Lambda event.
func Handler(ctx context.Context, request events.APIGatewayProxyRequest) (events.APIGatewayProxyResponse, error) {

	ctx, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()

	// Parse URL from query parameters
	url := request.QueryStringParameters["url"]
	if url == "" {
		return events.APIGatewayProxyResponse{
			StatusCode: http.StatusBadRequest,
			Body:       `{"error": "Missing 'url' parameter"}`,
		}, nil
	}
	// Scrape the Headline
	sectionDoc, err := FetchHTML(url)
	if err != nil {
		log.Printf("Error fetching section HTML: %v", err)
		return events.APIGatewayProxyResponse{
			StatusCode: http.StatusInternalServerError,
			Body:       fmt.Sprintf(`{"error": "Error fetching section HTML: %v"}`, err),
		}, nil
	}

	// Scrape the headline links
	headlineLinks, err := ScrapeHeadlines(sectionDoc)
	if err != nil {
		log.Printf("Error scraping headlines: %v", err)
		return events.APIGatewayProxyResponse{
			StatusCode: http.StatusInternalServerError,
			Body:       fmt.Sprintf(`{"error": "Error scraping headlines: %v"}`, err),
		}, nil
	}

	var articles []NewsArticle
	results := make(chan NewsArticle, len(headlineLinks))
	var wg sync.WaitGroup

	for _, link := range headlineLinks {
		wg.Add(1)
		go func(url string) {
			defer wg.Done()
			article, err := ScrapeArticle(url)
			if err != nil {
				log.Printf("Error scraping article: %v", err)
				return
			}
			results <- article
		}(link)
	}

	// Wait for all goroutines to complete
	wg.Wait()
	close(results)

	// Collect results
	for article := range results {
		articles = append(articles, article)
	}

	if len(articles) == 0 {
		log.Println("No articles scraped")
		return events.APIGatewayProxyResponse{
			StatusCode: http.StatusInternalServerError,
			Body:       fmt.Sprintf(`{"error": "Error scraping article: %v"}`, err),
		}, nil
	}

	// Convert article to JSON
	responseBody, err := json.Marshal(articles)
	if err != nil {
		log.Printf("Error encoding JSON: %v", err)
		return events.APIGatewayProxyResponse{
			StatusCode: http.StatusInternalServerError,
			Body:       fmt.Sprintf(`{"error": "Failed to encoding JSON: %v"}`, err),
		}, nil
	}

	return events.APIGatewayProxyResponse{
		StatusCode: http.StatusOK,
		Body:       string(responseBody),
		Headers: map[string]string{
			"Content-Type": "application/json",
		},
	}, nil
}

func HandlerTest(url string) {

	// Scrape the Headline
	sectionDoc, err := FetchHTML(url)
	if err != nil {
		log.Printf("Error fetching section HTML: %v", err)
	}

	// Scrape the headline links
	headlineLinks, err := ScrapeHeadlines(sectionDoc)
	if err != nil {
		log.Printf("Error scraping headlines: %v", err)
	}

	var articles []NewsArticle
	results := make(chan NewsArticle, len(headlineLinks))
	var wg sync.WaitGroup

	for _, link := range headlineLinks {
		wg.Add(1)
		go func(url string) {
			defer wg.Done()
			article, err := ScrapeArticle(url)
			if err != nil {
				log.Printf("Error scraping article: %v", err)
				return
			}
			results <- article
		}(link)
	}

	// Wait for all goroutines to complete
	wg.Wait()
	close(results)

	// Collect results
	for article := range results {
		articles = append(articles, article)
	}

	if len(articles) == 0 {
		log.Println("No articles scraped")
	}

	// Convert article to JSON
	responseBody, err := json.Marshal(articles)
	if err != nil {
		log.Printf("Error encoding JSON: %v", err)
	}

	fmt.Println(string(responseBody))

}

func main() {
	lambda.Start(Handler)
}
