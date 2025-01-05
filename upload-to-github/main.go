package main

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"path"
	"strings"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/google/go-github/v45/github"
	"github.com/joho/godotenv"
	"golang.org/x/oauth2"
)

type S3Downloader struct {
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

func main() {
	lambda.Start(Handler)
}

func (d *S3Downloader) ListFiles(ctx context.Context, prefix string) ([]string, error) {
	var files []string
	paginator := s3.NewListObjectsV2Paginator(d.Client, &s3.ListObjectsV2Input{
		Bucket: aws.String(d.BucketName),
		Prefix: aws.String(prefix),
	})

	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to list files: %v", err)
		}

		for _, obj := range page.Contents {
			files = append(files, *obj.Key)
		}
	}

	return files, nil
}

func (d *S3Downloader) DownloadFile(ctx context.Context, key string) ([]byte, error) {
	output, err := d.Client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(d.BucketName),
		Key:    aws.String(key),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to download file from S3: %v", err)
	}
	defer output.Body.Close()

	var buf bytes.Buffer
	_, err = buf.ReadFrom(output.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read S3 file content: %v", err)
	}

	return buf.Bytes(), nil
}

type GitHubUploader struct {
	Client *github.Client
	Owner  string
	Repo   string
}

func (u *GitHubUploader) UploadFiles(ctx context.Context, files map[string][]byte, commitMessage string) error {
	// Get the reference to the HEAD of the default branch (e.g., main)
	ref, _, err := u.Client.Git.GetRef(ctx, u.Owner, u.Repo, "heads/main")
	if err != nil {
		return fmt.Errorf("failed to get HEAD reference: %v", err)
	}

	// Get the current tree of the default branch
	baseTree, _, err := u.Client.Git.GetTree(ctx, u.Owner, u.Repo, *ref.Object.SHA, true)
	if err != nil {
		return fmt.Errorf("failed to get base tree: %v", err)
	}

	// Create a list of tree entries for the new files
	var entries []*github.TreeEntry
	for filePath, content := range files {
		entries = append(entries, &github.TreeEntry{
			Path:    github.String(filePath),
			Type:    github.String("blob"),
			Content: github.String(string(content)),
			Mode:    github.String("100644"), // Regular file mode
		})
	}

	// Create a new tree based on the current tree
	newTree, _, err := u.Client.Git.CreateTree(ctx, u.Owner, u.Repo, *baseTree.SHA, entries)
	if err != nil {
		return fmt.Errorf("failed to create tree: %v", err)
	}

	// Create a new commit
	newCommit := &github.Commit{
		Message: github.String(commitMessage),
		Tree:    newTree,
		Parents: []*github.Commit{{SHA: ref.Object.SHA}},
	}
	commit, _, err := u.Client.Git.CreateCommit(ctx, u.Owner, u.Repo, newCommit)
	if err != nil {
		return fmt.Errorf("failed to create commit: %v", err)
	}

	// Update the reference to point to the new commit
	ref.Object.SHA = commit.SHA
	_, _, err = u.Client.Git.UpdateRef(ctx, u.Owner, u.Repo, ref, false)
	if err != nil {
		return fmt.Errorf("failed to update HEAD reference: %v", err)
	}

	log.Printf("Successfully created commit: %s", *commit.SHA)
	return nil
}

// UploadFile uploads or updates a file to GitHub
func (u *GitHubUploader) UploadFile(ctx context.Context, path string, content []byte) error {

	// Check if file exists
	fileContent, _, resp, err := u.Client.Repositories.GetContents(ctx, u.Owner, u.Repo, path, nil)
	if err != nil && resp.StatusCode != http.StatusNotFound {
		return fmt.Errorf("failed to check file existence: %v", err)
	}

	message := "Add file: " + path
	if resp.StatusCode == http.StatusNotFound {
		// File does not exist, create new file
		opts := &github.RepositoryContentFileOptions{
			Message: github.String(message),
			Content: content,
		}
		_, _, err = u.Client.Repositories.CreateFile(ctx, u.Owner, u.Repo, path, opts)
		if err != nil {
			return fmt.Errorf("failed to create file: %v", err)
		}
		log.Printf("File %s successfully created on GitHub\n", path)
	} else {
		// File exists, update it
		sha := fileContent.GetSHA()
		opts := &github.RepositoryContentFileOptions{
			Message: github.String(message),
			Content: content,
			SHA:     github.String(sha),
		}
		_, _, err = u.Client.Repositories.UpdateFile(ctx, u.Owner, u.Repo, path, opts)
		if err != nil {
			return fmt.Errorf("failed to update file: %v", err)
		}
		log.Printf("File %s successfully updated on GitHub\n", path)
	}

	return nil
}

func Handler(ctx context.Context, request events.APIGatewayProxyRequest) (events.APIGatewayProxyResponse, error) {

	// 1. 환경 변수 불러오기
	awsRegion := os.Getenv("AWS_REGION")
	bucketName := os.Getenv("S3_BUCKET_NAME")
	githubToken := os.Getenv("TOKEN_GITHUB")
	owner := os.Getenv("OWNER_GITHUB")
	repo := os.Getenv("REPO_GITHUB")

	// 환경 변수 검증
	if awsRegion == "" || bucketName == "" || githubToken == "" || owner == "" || repo == "" {
		log.Fatalf("One or more required environment variables are missing.")
	}

	ctx = context.Background()

	// 1. S3 설정
	cfg, err := config.LoadDefaultConfig(ctx, config.WithRegion(awsRegion))
	if err != nil {
		log.Fatalf("failed to load AWS config: %v", err)
	}

	s3Client := s3.NewFromConfig(cfg)
	downloader := S3Downloader{
		Client:     s3Client,
		BucketName: bucketName,
	}

	// 2. GitHub 설정

	ts := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: githubToken})
	tc := oauth2.NewClient(ctx, ts)
	githubClient := github.NewClient(tc)

	uploader := GitHubUploader{
		Client: githubClient,
		Owner:  owner,
		Repo:   repo,
	}

	// 3. S3에서 파일 목록 가져오기
	today := time.Now().Format("2006-01-02")
	prefix := fmt.Sprintf("news/%s/", today)
	files, err := downloader.ListFiles(ctx, prefix)
	if err != nil {
		log.Fatalf("failed to list files in S3: %v", err)
	}

	// 4. 모든 파일 다운로드 및 GitHub 업로드 준비
	fileContents := make(map[string][]byte)
	for _, fileKey := range files {
		fileContent, err := downloader.DownloadFile(ctx, fileKey)
		if err != nil {
			log.Printf("failed to download file %s: %v", fileKey, err)
			continue
		}

		githubFilePath := path.Join(today, strings.TrimPrefix(fileKey, prefix))
		fileContents[githubFilePath] = fileContent
	}

	// 5. 한 번의 커밋으로 모든 파일 업로드
	if len(fileContents) > 0 {
		err = uploader.UploadFiles(ctx, fileContents, fmt.Sprintf("Add: 오늘의 기사 추가(%s)", today))
		if err != nil {
			log.Printf("failed to upload files to GitHub: %v", err)
			return events.APIGatewayProxyResponse{
				StatusCode: http.StatusInternalServerError,
				Body:       fmt.Sprintf(`{"error": "Failed to upload files: %v"}`, err),
			}, nil
		}
	}

	return events.APIGatewayProxyResponse{
		StatusCode: http.StatusOK,
		Body:       `{"message": "Files uploaded successfully in a single commit"}`,
	}, nil
}
