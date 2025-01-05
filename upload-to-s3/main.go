package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/joho/godotenv"
)

// S3Uploader uploads files to S3
type S3Uploader struct {
	Client     *s3.Client
	BucketName string
}

func init() {
	// .env 파일 로드 (로컬 환경에서만 사용)
	if _, isLambda := os.LookupEnv("LAMBDA_TASK_ROOT"); !isLambda {
		if err := godotenv.Load(); err != nil {
			log.Println("No .env file found. Falling back to system environment variables.")
		}
	}
}

// Upload uploads a file to S3
func (u *S3Uploader) Upload(ctx context.Context, key string, content []byte) error {
	_, err := u.Client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(u.BucketName),
		Key:         aws.String(key),
		Body:        bytes.NewReader(content),
		ContentType: aws.String("text/markdown"), // 마크다운 파일 MIME 타입
	})
	return err
}

// LambdaHandler handles the Lambda event
func LambdaHandler(ctx context.Context, request events.APIGatewayProxyRequest) (events.APIGatewayProxyResponse, error) {

	category, exist := request.Headers["x-category-sniij"]
	if !exist {
		log.Fatalf("failed to get x-category-sniij")
	}
	// 요청 본문 디코딩
	var markdownContent []byte
	var err error

	// Base64 디코딩 여부 확인
	if request.IsBase64Encoded {
		markdownContent, err = base64.StdEncoding.DecodeString(request.Body)
		if err != nil {
			log.Printf("Failed to decode Base64 request body: %v", err)
			return events.APIGatewayProxyResponse{
				StatusCode: 400,
				Body:       `{"error": "Invalid Base64 encoded request body"}`,
			}, nil
		}
	} else {
		markdownContent = []byte(request.Body)
	}

	if len(markdownContent) == 0 {
		log.Printf("Request body is empty")
		return events.APIGatewayProxyResponse{
			StatusCode: 400,
			Body:       `{"error": "Request body is empty"}`,
		}, nil
	}
	// S3 설정 초기화
	cfg, err := config.LoadDefaultConfig(ctx, config.WithRegion("ap-northeast-2"))
	if err != nil {
		log.Fatalf("failed to load AWS config: %v", err)
	}

	s3Client := s3.NewFromConfig(cfg)
	uploader := S3Uploader{
		Client:     s3Client,
		BucketName: os.Getenv("S3_BUCKET_NAME"),
	}

	filename := fmt.Sprintf("news/%s/%s_%s.md", time.Now().Format("2006-01-02"), time.Now().Format("2006-01-02"), category)

	// 파일 업로드
	err = uploader.Upload(ctx, filename, markdownContent)
	if err != nil {
		log.Printf("failed to upload file: %v", err)
		return events.APIGatewayProxyResponse{
			StatusCode: 500,
			Body:       fmt.Sprintf(`{"error": "Failed to upload file: %v"}`, err),
		}, nil
	}

	// 성공 응답 반환
	return events.APIGatewayProxyResponse{
		StatusCode: 200,
		Body:       fmt.Sprintf(`{"message": "File uploaded successfully", "filename": "%s"}`, filename),
	}, nil
}

func main() {
	lambda.Start(LambdaHandler)
}
