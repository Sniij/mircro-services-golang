package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
	"github.com/joho/godotenv"
	"github.com/sashabaranov/go-openai"
)

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

func ChatGPT(gptRequest GPTRequest, client *openai.Client) (string, error) {
	ctx := context.Background()

	// Create a prompt for summarization
	var messages []openai.ChatCompletionMessage
	// messages = append(messages, openai.ChatCompletionMessage{
	// 	Role:    "system",
	// 	Content: "",
	// })

	messages = append(messages, openai.ChatCompletionMessage{
		Role:    "user",
		Content: fmt.Sprintf("%s :\n\n%s", gptRequest.Prompt, gptRequest.Content),
	})

	contentResp, err := client.CreateChatCompletion(ctx, openai.ChatCompletionRequest{
		Model:    "gpt-3.5-turbo",
		Messages: messages,
	})
	if err != nil {
		log.Fatalf("%v", err)
	}

	return contentResp.Choices[0].Message.Content, nil
}

// Handler processes the Lambda event.
func Handler(ctx context.Context, request events.APIGatewayProxyRequest) (events.APIGatewayProxyResponse, error) {

	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	var req GPTRequest

	err := json.Unmarshal([]byte(request.Body), &req)
	if err != nil {
		log.Println(request)
		log.Printf("Invalid request body: %v", err)
		return events.APIGatewayProxyResponse{
			StatusCode: http.StatusBadRequest,
			Body:       fmt.Sprintf(`{"error": "Invalid request body: %v"}`, err),
		}, nil
	}

	// Initialize OpenAI client
	apiKey := os.Getenv("API_KEY")
	client := openai.NewClient(apiKey)

	gptResponse, err := ChatGPT(req, client)
	if err != nil {
		log.Printf("Failed to gpt connection: %v", err)
		return events.APIGatewayProxyResponse{
			StatusCode: http.StatusInternalServerError,
			Body:       fmt.Sprintf(`{"error": "Failed to gpt connection: %v"}`, err),
		}, nil
	}

	return events.APIGatewayProxyResponse{
		StatusCode: http.StatusOK,
		Body:       string(gptResponse),
		Headers: map[string]string{
			"Content-Type": "text/plain",
		},
	}, nil
}

func main() {
	lambda.Start(Handler)
}
