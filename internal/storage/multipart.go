package storage

import (
	"context"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

// Part represents a completed multipart upload part.
type Part struct {
	PartNumber int32
	ETag       string
}

func CreateMultipartUpload(ctx context.Context, key, contentType string) (string, error) {
	if Client == nil {
		return "", fmt.Errorf("storage: not connected")
	}
	out, err := Client.CreateMultipartUpload(ctx, &s3.CreateMultipartUploadInput{
		Bucket:      aws.String(Bucket),
		Key:         aws.String(key),
		ContentType: aws.String(contentType),
	})
	if err != nil {
		return "", fmt.Errorf("storage: create multipart %s: %w", key, err)
	}
	return aws.ToString(out.UploadId), nil
}

func ListMultipartParts(ctx context.Context, key, uploadID string) ([]types.Part, error) {
	if Client == nil {
		return nil, fmt.Errorf("storage: not connected")
	}
	out, err := Client.ListParts(ctx, &s3.ListPartsInput{
		Bucket:   aws.String(Bucket),
		Key:      aws.String(key),
		UploadId: aws.String(uploadID),
	})
	if err != nil {
		return nil, fmt.Errorf("storage: list parts %s/%s: %w", key, uploadID, err)
	}
	return out.Parts, nil
}

func PresignUploadPart(ctx context.Context, key, uploadID string, partNumber int32) (string, error) {
	if PresignClient == nil {
		return "", fmt.Errorf("storage: not connected")
	}
	req, err := PresignClient.PresignUploadPart(ctx, &s3.UploadPartInput{
		Bucket:     aws.String(Bucket),
		Key:        aws.String(key),
		UploadId:   aws.String(uploadID),
		PartNumber: aws.Int32(partNumber),
	}, s3.WithPresignExpires(1*time.Hour))
	if err != nil {
		return "", fmt.Errorf("storage: presign part %d/%s: %w", partNumber, key, err)
	}
	return req.URL, nil
}

func AbortMultipartUpload(ctx context.Context, key, uploadID string) error {
	if Client == nil {
		return fmt.Errorf("storage: not connected")
	}
	_, err := Client.AbortMultipartUpload(ctx, &s3.AbortMultipartUploadInput{
		Bucket:   aws.String(Bucket),
		Key:      aws.String(key),
		UploadId: aws.String(uploadID),
	})
	if err != nil {
		return fmt.Errorf("storage: abort %s/%s: %w", key, uploadID, err)
	}
	return nil
}

func CompleteMultipartUpload(ctx context.Context, key, uploadID string, parts []Part) (string, error) {
	if Client == nil {
		return "", fmt.Errorf("storage: not connected")
	}
	completed := make([]types.CompletedPart, len(parts))
	for i, p := range parts {
		completed[i] = types.CompletedPart{
			PartNumber: aws.Int32(p.PartNumber),
			ETag:       aws.String(p.ETag),
		}
	}
	out, err := Client.CompleteMultipartUpload(ctx, &s3.CompleteMultipartUploadInput{
		Bucket:   aws.String(Bucket),
		Key:      aws.String(key),
		UploadId: aws.String(uploadID),
		MultipartUpload: &types.CompletedMultipartUpload{
			Parts: completed,
		},
	})
	if err != nil {
		return "", fmt.Errorf("storage: complete %s/%s: %w", key, uploadID, err)
	}
	return aws.ToString(out.Location), nil
}
