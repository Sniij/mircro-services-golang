package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"sync"

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

// GPTRequest represents the payload for the GPT server.
type GPTRequest struct {
	Content string `json:"content"`
	Prompt  string `json:"prompt"`
}

func init() {
	// .env 파일 로드 (로컬 환경에서만 사용)
	if _, isLambda := os.LookupEnv("LAMBDA_TASK_ROOT"); !isLambda {
		if err := godotenv.Load(); err != nil {
			log.Println("No .env file found. Falling back to system environment variables.")
		}
	}
}

// FetchGPT processes text using the custom GPT server.
func FetchGPT(gptRequest GPTRequest) (string, error) {
	serverURL := os.Getenv("GPT_SERVER")

	body, err := json.Marshal(gptRequest)
	if err != nil {
		return "", fmt.Errorf("failed to marshal GPT request: %v", err)
	}

	req, err := http.NewRequest("POST", serverURL, bytes.NewBuffer(body))
	if err != nil {
		return "", fmt.Errorf("failed to create HTTP request: %v", err)
	}

	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{}
	res, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to send HTTP request: %v", err)
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		return "", fmt.Errorf("GPT server returned status code %d", res.StatusCode)
	}

	gptResponse, err := io.ReadAll(res.Body)
	if err != nil {
		return "", fmt.Errorf("failed to decode GPT response: %v", err)
	}

	return string(gptResponse), nil
}

// ConvertToMarkdown converts an article to Markdown format.
func ConvertToMarkdown(article NewsArticle) []byte {
	title := fmt.Sprintf("# **제목: %s**", article.Title)
	content := fmt.Sprintf("내용: %s", article.Content)

	date := fmt.Sprintf("**날짜: %s**", article.Date)

	return []byte(fmt.Sprintf("%s\n\n  %s\n\n  %s", title, content, date))
}

// Handler processes the Lambda event.
func Handler(ctx context.Context, request events.APIGatewayProxyRequest) (events.APIGatewayProxyResponse, error) {
	var article NewsArticle
	if err := json.Unmarshal([]byte(request.Body), &article); err != nil {
		return events.APIGatewayProxyResponse{
			StatusCode: http.StatusBadRequest,
			Body:       `{"error": "Invalid JSON input"}`,
		}, nil
	}

	var wg sync.WaitGroup

	wg.Add(2)

	go func() {
		defer wg.Done()
		cleanedContent, err := FetchGPT(GPTRequest{Content: article.Content, Prompt: os.Getenv("PROMPT_CONTENT_1")})
		if err != nil {
			log.Printf("Error processing article with GPT that content: %v", err)
			return
		}
		cleanedContent, err = FetchGPT(GPTRequest{Content: article.Content, Prompt: os.Getenv("PROMPT_CONTENT_2")})
		if err != nil {
			log.Printf("Error processing article with GPT that content: %v", err)
			return
		}

		cleanedContent, err = FetchGPT(GPTRequest{Content: article.Content, Prompt: os.Getenv("PROMPT_CONTENT_3")})
		if err != nil {
			log.Printf("Error processing article with GPT that content: %v", err)
			return
		}

		article.Content = cleanedContent
	}()
	go func() {
		defer wg.Done()
		cleanedDate, err := FetchGPT(GPTRequest{Content: article.Date, Prompt: "다음 텍스트에서 날짜가 여러개 있으면 앞에 것만 선택해서 한 날짜만 남게 해주고, 'yyyy년 mm월 dd일 hh시 mm분' 포맷으로 수정해주세요. 예를 들어 '2025년 01월 04일 오후 3시 25분2025년 01월 04일 오후 4시 08분' 이런식으로 있다면 '2025년 01월 04일 오후 3시 25분'만 남게 해주세요."})
		if err != nil {
			log.Printf("Error processing article with GPT that date: %v", err)
			return
		}
		article.Date = cleanedDate
	}()

	wg.Wait()

	markdown := ConvertToMarkdown(article)

	if len(markdown) == 0 {
		return events.APIGatewayProxyResponse{
			StatusCode: http.StatusInternalServerError,
			Body:       `{"error": "No articles processed"}`,
		}, nil
	}

	return events.APIGatewayProxyResponse{
		StatusCode: http.StatusOK,
		Body:       string(markdown),
		Headers: map[string]string{
			"Content-Type": "text/plain",
		},
	}, nil
}

func main() {
	lambda.Start(Handler)
}
