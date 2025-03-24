package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/bootdotdev/learn-file-storage-s3-golang-starter/internal/database"
)

func generatePresignedURL(s3Client *s3.Client, bucket, key string, expireTime time.Duration) (string, error) {
	presignClient := s3.NewPresignClient(s3Client)
	params := s3.GetObjectInput{
		Bucket: &bucket,
		Key:    &key,
	}

	presignedURL, err := presignClient.PresignGetObject(context.Background(), &params, s3.WithPresignExpires(expireTime))
	if err != nil {
		return "", fmt.Errorf("couldn't presign URL: %v", err)
	}

	return presignedURL.URL, nil
}

func (cfg *apiConfig) dbVideoToSignedVideo(video database.Video) (database.Video, error) {
	if video.VideoURL == nil {
		return video, nil
	}

	parts := strings.Split(*video.VideoURL, ",")
	if len(parts) != 2 {
		return video, fmt.Errorf("video URL does not have the expected format: %s", *video.VideoURL)
	}

	bucket := parts[0]
	key := parts[1]

	presignedURL, err := generatePresignedURL(cfg.s3Client, bucket, key, 1*time.Hour)
	if err != nil {
		return video, fmt.Errorf("couldn't presign URL: %v", err)
	}

	presignedVideo := video
	presignedVideo.VideoURL = &presignedURL

	return presignedVideo, nil
}
