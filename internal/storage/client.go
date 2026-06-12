package storage

import (
	"context"
	"os"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

var (
	Client        *s3.Client
	PresignClient *s3.PresignClient
	Bucket        string
)

func Connect(ctx context.Context) error {
	Bucket = os.Getenv("HETZNER_BUCKET")

	cfg, err := config.LoadDefaultConfig(ctx,
		config.WithRegion("eu-central"),
		config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(
			os.Getenv("HETZNER_ACCESS_KEY"),
			os.Getenv("HETZNER_SECRET_KEY"),
			"",
		)),
		config.WithEndpointResolverWithOptions(
			aws.EndpointResolverWithOptionsFunc(func(service, region string, opts ...interface{}) (aws.Endpoint, error) {
				return aws.Endpoint{
					URL:               os.Getenv("HETZNER_S3_ENDPOINT"),
					HostnameImmutable: true,
				}, nil
			}),
		),
	)
	if err != nil {
		return err
	}

	Client = s3.NewFromConfig(cfg)
	PresignClient = s3.NewPresignClient(Client)
	return nil
}

// EnsureCORS sets a permissive CORS policy on the bucket so browsers can PUT
// parts directly to presigned S3 URLs from any origin.
func EnsureCORS(ctx context.Context) error {
	_, err := Client.PutBucketCors(ctx, &s3.PutBucketCorsInput{
		Bucket: aws.String(Bucket),
		CORSConfiguration: &types.CORSConfiguration{
			CORSRules: []types.CORSRule{
				{
					AllowedOrigins: []string{"*"},
					AllowedMethods: []string{"GET", "PUT", "POST", "DELETE", "HEAD"},
					AllowedHeaders: []string{"*"},
					ExposeHeaders:  []string{"ETag"},
					MaxAgeSeconds:  aws.Int32(3000),
				},
			},
		},
	})
	return err
}
