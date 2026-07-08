package storage

import (
	"context"
	"fmt"
	"io"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/s3/manager"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// GetObjectStream returns the object body for streaming; caller closes it.
func GetObjectStream(ctx context.Context, key string) (io.ReadCloser, error) {
	if Client == nil {
		return nil, fmt.Errorf("storage: not connected")
	}
	out, err := Client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(Bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return nil, fmt.Errorf("storage: get %s: %w", key, err)
	}
	return out.Body, nil
}

// countingReader counts bytes as they pass through.
type countingReader struct {
	r io.Reader
	n int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}

// UploadStream uploads r to key via managed multipart upload (constant
// memory) and returns the number of bytes written.
func UploadStream(ctx context.Context, key, contentType string, r io.Reader) (int64, error) {
	if Client == nil {
		return 0, fmt.Errorf("storage: not connected")
	}
	cr := &countingReader{r: r}
	uploader := manager.NewUploader(Client)
	_, err := uploader.Upload(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(Bucket),
		Key:         aws.String(key),
		Body:        cr,
		ContentType: aws.String(contentType),
	})
	if err != nil {
		return 0, fmt.Errorf("storage: upload stream %s: %w", key, err)
	}
	return cr.n, nil
}
