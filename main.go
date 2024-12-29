package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/robfig/cron/v3"
)

func getEnv(name string) string {
	envVar := os.Getenv(name)
	trimmedVar := strings.TrimSpace(envVar)
	trimmedVar = strings.Trim(trimmedVar, "/")
	return trimmedVar
}

var DB_HOST = getEnv("DB_HOST")
var DB_PORT = getEnv("DB_PORT")
var REMOTE_FOLDER = getEnv("REMOTE_FOLDER")
var S3_ACCESS_KEY = getEnv("S3_ACCESS_KEY")
var S3_BUCKET_NAME = getEnv("S3_BUCKET_NAME")
var S3_ENDPOINT = getEnv("S3_ENDPOINT")
var S3_REGION = getEnv("S3_REGION")
var S3_SECRET_KEY = getEnv("S3_SECRET_KEY")
var SCHEDULE = getEnv("SCHEDULE")

var POSTGRES_PASSWORD = getEnv("POSTGRES_PASSWORD")
var POSTGRES_USER = getEnv("POSTGRES_USER")

var backupsPath string = "/tmp/backups"

var mu sync.Mutex

type Metadata struct {
	LastSnapshot *string `json:"lastSnapshot"`
}

func main() {
	noEmptyValues([]string{
		DB_HOST,
		DB_PORT,
		REMOTE_FOLDER,
		S3_ACCESS_KEY,
		S3_BUCKET_NAME,
		S3_ENDPOINT,
		S3_REGION,
		S3_SECRET_KEY,
		SCHEDULE,
		POSTGRES_PASSWORD,
		POSTGRES_USER,
	})

	validateCronExpression(SCHEDULE)

	cfg, err := config.LoadDefaultConfig(context.Background(),
		config.WithCredentialsProvider(aws.CredentialsProviderFunc(func(ctx context.Context) (aws.Credentials, error) {
			return aws.Credentials{
				AccessKeyID:     S3_ACCESS_KEY,
				SecretAccessKey: S3_SECRET_KEY,
			}, nil
		})),
		config.WithRegion(S3_REGION),
		config.WithEndpointResolver(aws.EndpointResolverFunc(func(service, region string) (aws.Endpoint, error) {
			return aws.Endpoint{
				URL: S3_ENDPOINT,
			}, nil
		})),
	)
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	s3Client := s3.NewFromConfig(cfg)

	c := cron.New()
	c.AddFunc(SCHEDULE, func() {
		backup(s3Client)
	})
	c.Start()

	select {}
}

func noEmptyValues(envVars []string) {
	for _, v := range envVars {
		if v == "" {
			log.Fatalf("Environment variables cannot be empty.")
		}
	}
}

func validateCronExpression(cronExpr string) {
	schedule, err := cron.ParseStandard(cronExpr)
	if err != nil {
		log.Fatalf("%v", err)
	}

	now := time.Now()
	next := schedule.Next(now)
	nextAfter := schedule.Next(next)

	interval := nextAfter.Sub(next)

	if interval < 5*time.Minute && interval > 24*time.Hour {
		log.Fatalf("The schedule must be between 5 minutes and 24 hours.")
	}
}

func getBackupPath(p string) *string {
	path := backupsPath
	if p != "" {
		path = path + "/" + p
	}
	return &path
}

func backup(s3Client *s3.Client) {
	mu.Lock()
	defer mu.Unlock()

	createBackupFolder()

	metadata := GetMetadata(s3Client)

	if metadata == nil {
		log.Println("Failed to get metadata. Proceeding with a full backup.")
	}

	var backupManifest *string

	if metadata != nil && metadata.LastSnapshot != nil {
		backupManifest = getBackupPath("backup_manifest")

		err := DownloadFile(s3Client, fmt.Sprintf("%s/%s/backup_manifest", REMOTE_FOLDER, *metadata.LastSnapshot), *backupManifest)

		if err != nil {
			log.Println("Failed to download backup_manifest.")
			return
		}
	}

	snapshot := runCommand(backupManifest)

	if snapshot == nil {
		return
	}

	backupFolderPath := *getBackupPath(*snapshot)
	saveToS3Path := fmt.Sprintf("%s/%s", REMOTE_FOLDER, *snapshot)

	if err := UploadFolder(s3Client, backupFolderPath, saveToS3Path); err != nil {
		log.Printf("Failed to upload backup: %v", err)
		return
	}

	newMetadata := &Metadata{
		LastSnapshot: snapshot,
	}
	if err := SetMetadata(s3Client, newMetadata); err != nil {
		log.Printf("Failed to update metadata: %v", err)
		return
	}

	deleteBackupContents()

	fmt.Println("Backup successful.")
}

func GetMetadata(s3Client *s3.Client) *Metadata {
	objectKey := fmt.Sprintf("%s/metadata.json", REMOTE_FOLDER)

	resp, err := s3Client.GetObject(context.Background(), &s3.GetObjectInput{
		Bucket: aws.String(S3_BUCKET_NAME),
		Key:    aws.String(objectKey),
	})
	if err != nil {
		return nil
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil
	}

	var metadata Metadata
	if err := json.Unmarshal(body, &metadata); err != nil {
		return nil
	}

	return &metadata
}

func SetMetadata(s3Client *s3.Client, metadata *Metadata) error {
	objectKey := fmt.Sprintf("%s/metadata.json", REMOTE_FOLDER)

	data, err := json.Marshal(metadata)
	if err != nil {
		return fmt.Errorf("Failed to marshal metadata: %w", err)
	}

	_, err = s3Client.PutObject(context.Background(), &s3.PutObjectInput{
		Bucket: aws.String(S3_BUCKET_NAME),
		Key:    aws.String(objectKey),
		Body:   bytes.NewReader(data),
	})
	if err != nil {
		return fmt.Errorf("Failed to upload metadata.json: %w", err)
	}

	return nil
}

func DownloadFile(s3Client *s3.Client, objectKey, localPath string) error {
	resp, err := s3Client.GetObject(context.Background(), &s3.GetObjectInput{
		Bucket: aws.String(S3_BUCKET_NAME),
		Key:    aws.String(objectKey),
	})
	if err != nil {
		return fmt.Errorf("Failed to fetch object %s: %w", objectKey, err)
	}
	defer resp.Body.Close()

	outFile, err := os.Create(localPath)
	if err != nil {
		return fmt.Errorf("Failed to create local file %s: %w", localPath, err)
	}
	defer outFile.Close()

	if _, err := io.Copy(outFile, resp.Body); err != nil {
		return fmt.Errorf("Failed to save file %s: %w", localPath, err)
	}

	return nil
}

func runCommand(backupManifest *string) *string {
	unixTimestamp := time.Now().Unix()
	snapshot := fmt.Sprintf("snapshot-%d", unixTimestamp)
	pgData := *getBackupPath(snapshot)

	args := []string{
		fmt.Sprintf("--host=%s", DB_HOST),
		fmt.Sprintf("--port=%s", DB_PORT),
		fmt.Sprintf("--username=%s", POSTGRES_USER),
		fmt.Sprintf("--pgdata=%s", pgData),
		"--format=t",
		"--gzip",
		"--progress",
	}

	if backupManifest != nil {
		args = append(args, fmt.Sprintf("--incremental=%s", *backupManifest))
	}

	cmd := exec.Command("pg_basebackup", args...)

	cmd.Env = append(os.Environ(), fmt.Sprintf("PGPASSWORD=%s", POSTGRES_PASSWORD))

	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		fmt.Printf("Command execution failed: %v\n", err)
		return nil
	}

	fmt.Println("Backup created successfully!")
	return &snapshot
}

func UploadFolder(s3Client *s3.Client, backupFolderPath, saveToS3Path string) error {
	err := filepath.Walk(backupFolderPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return fmt.Errorf("Error accessing file %s: %w", path, err)
		}

		if info.IsDir() {
			return nil
		}

		relativePath := strings.TrimPrefix(path, backupFolderPath)
		relativePath = strings.TrimPrefix(relativePath, string(os.PathSeparator))
		s3Key := filepath.Join(saveToS3Path, relativePath)

		if err := uploadFile(s3Client, S3_BUCKET_NAME, path, s3Key); err != nil {
			return fmt.Errorf("Failed to upload file %s: %w", path, err)
		}

		fmt.Printf("Uploaded: %s -> %s\n", path, s3Key)
		return nil
	})

	if err != nil {
		return fmt.Errorf("Failed to upload folder: %w", err)
	}

	return nil
}

func uploadFile(s3Client *s3.Client, bucketName, filePath, key string) error {
	file, err := os.Open(filePath)
	if err != nil {
		return fmt.Errorf("Failed to open file %s: %w", filePath, err)
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return fmt.Errorf("Failed to get file info for %s: %w", filePath, err)
	}

	fileSize := info.Size()

	_, err = s3Client.PutObject(context.Background(), &s3.PutObjectInput{
		Bucket:        aws.String(bucketName),
		Key:           aws.String(key),
		Body:          file,
		ContentLength: &fileSize,
	})
	if err != nil {
		return fmt.Errorf("Failed to upload file %s: %w", filePath, err)
	}

	return nil
}

func createBackupFolder() error {
	if _, err := os.Stat(backupsPath); os.IsNotExist(err) {
		err := os.MkdirAll(backupsPath, 0755)
		if err != nil {
			return fmt.Errorf("Failed to create folder: %v", err)
		}
		fmt.Println("Folder created:", backupsPath)
	} else {
		fmt.Println("Folder already exists:", backupsPath)
	}
	return nil
}

func deleteBackupContents() error {
	dir, err := os.Open(backupsPath)
	if err != nil {
		return fmt.Errorf("Failed to open folder: %v", err)
	}
	defer dir.Close()

	contents, err := dir.Readdirnames(-1)
	if err != nil {
		return fmt.Errorf("Failed to read folder contents: %v", err)
	}

	for _, item := range contents {
		fullPath := filepath.Join(backupsPath, item)
		err := os.RemoveAll(fullPath)
		if err != nil {
			return fmt.Errorf("Failed to delete %s: %v", fullPath, err)
		}
		fmt.Println("Deleted:", fullPath)
	}

	fmt.Println("Deleted all contents inside:", backupsPath)
	return nil
}
