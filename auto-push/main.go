package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	netURL "net/url"
	"os"
	"regexp"
	"strconv"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
	"github.com/joho/godotenv"
)

type NewsArticle struct {
	Title   string `json:"title"`
	Content string `json:"content"`
	Date    string `json:"date"`
}
type S3Response struct {
	Message  string `json:"message"`
	Filename string `json:"filename"`
}

var httpClient = &http.Client{
	Timeout: 120 * time.Second,
}

func init() {
	// .env 파일 로드 (로컬 환경에서만 사용)
	if _, isLambda := os.LookupEnv("LAMBDA_TASK_ROOT"); !isLambda {
		if err := godotenv.Load(); err != nil {
			log.Println("No .env file found. Falling back to system environment variables.")
		}
	}
}

func main() {
	//HandlerTest()
	lambda.Start(Handler)
}

func Handler(ctx context.Context, request events.APIGatewayProxyRequest) (events.APIGatewayProxyResponse, error) {
	// 정치, 경제, 사회, IT/과학, 세계
	// Politics, Economy, Society, IT/Science, World
	urls := map[string]string{
		"politics": "https://news.naver.com/section/100",
		"economy":  "https://news.naver.com/section/101",
		"society":  "https://news.naver.com/section/102",
		"it":       "https://news.naver.com/section/105",
		"world":    "https://news.naver.com/section/104",
	}

	var wg sync.WaitGroup
	for category, url := range urls {
		wg.Add(1)
		go func(category, url string) {
			defer wg.Done()
			processArticles(url, category)
		}(category, url)
	}
	wg.Wait()
	UploadToGitHub()

	return events.APIGatewayProxyResponse{
		StatusCode: http.StatusOK,
	}, nil
}

func HandlerTest() {
	// 정치, 경제, 사회, IT/과학, 세계
	// Politics, Economy, Society, IT/Science, World
	urls := map[string]string{
		"politics": "https://news.naver.com/section/100",
	}

	var wg sync.WaitGroup
	for category, url := range urls {
		wg.Add(1)
		go func(category, url string) {
			defer wg.Done()
			processArticles(url, category)
		}(category, url)
	}
	wg.Wait()
	UploadToGitHub()

}

func processArticles(url, category string) {
	log.Printf("Start to process articles %s \n", category)

	articles, err := Scrape(url)
	if err != nil {
		log.Printf("Failed to get articles for %s: %v", category, err)
		return
	}

	var wg sync.WaitGroup
	for i, article := range articles {
		article := article
		wg.Add(1)
		go func(article NewsArticle, category string, i int) {
			defer wg.Done()
			markdown, err := ConvertToMarkdown(article)
			if err != nil {
				log.Printf("Failed to convert article to markdown for %s: %v", category, err)
				return
			}
			log.Printf("Successfully to Convert To Markdown: %s \n", category)

			UploadToS3(markdown, category, i)
			log.Printf("Successfully to Upload To S3: %s \n", category)
		}(article, category, i)
	}

	wg.Wait()
}
func Scrape(url string) ([]NewsArticle, error) {
	serverURL, err := netURL.QueryUnescape(os.Getenv("CRAWLING_SERVER"))
	if err != nil {
		return []NewsArticle{}, fmt.Errorf("failed to get server url: %v", err)
	}

	// HTTP 요청 생성
	req, err := http.NewRequest("GET", serverURL, nil)
	if err != nil {
		return []NewsArticle{}, fmt.Errorf("failed to create HTTP request: %v", err)
	}

	// 쿼리 파라미터 추가
	q := req.URL.Query()
	q.Add("url", url)
	req.URL.RawQuery = q.Encode()

	// 요청 실행
	res, err := httpClient.Do(req)
	if err != nil {
		return []NewsArticle{}, fmt.Errorf("failed to send HTTP request: %v", err)
	}
	defer res.Body.Close()

	// HTTP 응답 상태 코드 확인
	if res.StatusCode != http.StatusOK {
		return []NewsArticle{}, fmt.Errorf("server returned status code %d", res.StatusCode)
	}

	// 응답 본문 읽기
	body, err := io.ReadAll(res.Body)
	if err != nil {
		return []NewsArticle{}, fmt.Errorf("failed to read response body: %v", err)
	}

	// JSON 디코딩
	var articles []NewsArticle
	if err := json.Unmarshal(body, &articles); err != nil {
		return []NewsArticle{}, fmt.Errorf("Invalid JSON input: %v", err)
	}

	return articles, nil
}

func ConvertToMarkdown(article NewsArticle) ([]byte, error) {

	serverURL, err := netURL.QueryUnescape(os.Getenv("CONVERT_SERVER"))
	if err != nil {
		return []byte{}, fmt.Errorf("failed to get server url: %v", err)
	}
	// HTTP 요청 객체 생성
	reqBody, err := json.Marshal(article)
	if err != nil {
		return []byte{}, fmt.Errorf("failed to article request: %v", err)
	}

	// HTTP 요청 생성
	req, err := http.NewRequest("POST", serverURL, bytes.NewBuffer(reqBody))
	if err != nil {
		return []byte{}, fmt.Errorf("failed to create HTTP request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")

	// 요청 실행
	res, err := httpClient.Do(req)
	if err != nil {
		return []byte{}, fmt.Errorf("failed to send HTTP request: %v", err)
	}
	defer res.Body.Close()

	// HTTP 응답 상태 코드 확인
	if res.StatusCode != http.StatusOK {
		return []byte{}, fmt.Errorf("ConvertToMarkdown server returned status code %d", res.StatusCode)
	}

	// 응답 본문 읽기
	resBody, err := io.ReadAll(res.Body)
	if err != nil {
		return []byte{}, fmt.Errorf("failed to read response body: %v", err)
	}
	return resBody, nil
}
func cleanANSI(input string) string {
	// ANSI 이스케이프 코드 정규식
	ansiRegex := regexp.MustCompile(`\x1B\[[0-9;]*[a-zA-Z]`)
	return ansiRegex.ReplaceAllString(input, "")
}
func UploadToS3(markdown []byte, category string, i int) {
	if !utf8.Valid(markdown) {
		log.Printf("Input data is not valid UTF-8. Converting...")
		markdown = []byte(string(markdown))
	}
	cleanedMarkdown := cleanANSI(string(markdown))

	serverURL, err := netURL.QueryUnescape(os.Getenv("UPLOAD_TO_S3_SEVER"))
	if err != nil {
		log.Printf("failed to get server url: %v", err)
		return
	}

	// HTTP 요청 생성
	req, err := http.NewRequest("POST", serverURL, bytes.NewBuffer([]byte(cleanedMarkdown)))
	if err != nil {
		log.Printf("failed to create HTTP request: %v", err)
		return
	}
	category = category + "_" + strconv.Itoa(i)
	req.Header.Set("x-category-sniij", category)

	// 요청 실행
	res, err := httpClient.Do(req)
	if err != nil {
		log.Printf("failed to send HTTP request: %v", err)
		return
	}
	defer res.Body.Close()

	// HTTP 응답 상태 코드 확인
	if res.StatusCode != http.StatusOK {
		log.Printf("UploadToS3 server returned status code %d", res.StatusCode)
		return
	}

	// 응답 본문 읽기
	resBody, err := io.ReadAll(res.Body)
	if err != nil {
		log.Printf("failed to read response body: %v", err)
		return
	}

	var response S3Response
	if err := json.Unmarshal(resBody, &response); err != nil {
		log.Printf("Invalid JSON input: %v", err)
		return
	}
	log.Printf("S3 msg: %v, filename:%v", response.Message, response.Filename)
}

func UploadToGitHub() {
	serverURL, err := netURL.QueryUnescape(os.Getenv("UPLOAD_TO_GITHUB_SERVER"))
	if err != nil {
		log.Printf("failed to get server url: %v", err)
		return
	}

	// HTTP 요청 생성
	req, err := http.NewRequest("GET", serverURL, nil)
	if err != nil {
		log.Printf("failed to create HTTP request: %v", err)
		return
	}

	// 요청 실행
	res, err := httpClient.Do(req)
	if err != nil {
		log.Printf("failed to send HTTP request: %v", err)
		return
	}
	defer res.Body.Close()

	// HTTP 응답 상태 코드 확인
	if res.StatusCode != http.StatusOK {
		log.Printf("UploadToGitHub returned status code %d", res.StatusCode)
		return
	}

	log.Printf("Successfully Uploaded to GitHub")
}
