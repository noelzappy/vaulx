package storage

import (
	"context"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// PresignPUT returns a presigned URL for a direct browser PUT upload.
// The client must set Content-Type and Content-Length headers to match.
func PresignPUT(ctx context.Context, key, contentType string, size int64) (string, error) {
	if PresignClient == nil {
		return "", fmt.Errorf("storage: not connected")
	}
	req, err := PresignClient.PresignPutObject(ctx, &s3.PutObjectInput{
		Bucket:        aws.String(Bucket),
		Key:           aws.String(key),
		ContentType:   aws.String(contentType),
		ContentLength: aws.Int64(size),
	}, s3.WithPresignExpires(15*time.Minute))
	if err != nil {
		return "", fmt.Errorf("storage: presign PUT %s: %w", key, err)
	}
	return req.URL, nil
}

// PresignGET returns a presigned URL for a time-limited file download.
func PresignGET(ctx context.Context, key string) (string, error) {
	if PresignClient == nil {
		return "", fmt.Errorf("storage: not connected")
	}
	req, err := PresignClient.PresignGetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(Bucket),
		Key:    aws.String(key),
	}, s3.WithPresignExpires(15*time.Minute))
	if err != nil {
		return "", fmt.Errorf("storage: presign GET %s: %w", key, err)
	}
	return req.URL, nil
}

// PresignGETWithTTL returns a presigned URL for a file download with a custom TTL.
func PresignGETWithTTL(ctx context.Context, key string, ttl time.Duration) (string, error) {
	if PresignClient == nil {
		return "", fmt.Errorf("storage: not connected")
	}
	req, err := PresignClient.PresignGetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(Bucket),
		Key:    aws.String(key),
	}, s3.WithPresignExpires(ttl))
	if err != nil {
		return "", fmt.Errorf("storage: presign GET %s: %w", key, err)
	}
	return req.URL, nil
}

// DeleteObject permanently removes an object from S3/Hetzner storage.
func DeleteObject(ctx context.Context, key string) error {
	if Client == nil {
		return fmt.Errorf("storage: not connected")
	}
	_, err := Client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(Bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return fmt.Errorf("storage: delete %s: %w", key, err)
	}
	return nil
}
