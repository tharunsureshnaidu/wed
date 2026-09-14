package storage

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	awscfg "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// S3 stores media in an S3 bucket, as Java's S3UploadService does.
//
// ponytail: PutObject with the bytes streamed through this process, which is
// what Java does - not presigned uploads. Presigning is the better shape for
// large video and the Store interface leaves room for it, but changing it now
// would change the client contract for every existing upload.
type S3 struct {
	client *s3.Client
	bucket string
	// cdn is an optional CloudFront domain. Empty means serve straight from the
	// bucket, which is how this deployment is configured today.
	cdn string
}

// NewS3 builds a client from the ambient AWS configuration: region from
// AWS_S3_REGION (falling back to the SDK's own resolution) and credentials from
// the default chain, exactly as Java's S3Config leaves them to the SDK.
func NewS3(ctx context.Context, bucket string) (*S3, error) {
	var opts []func(*awscfg.LoadOptions) error
	if region := os.Getenv("AWS_S3_REGION"); region != "" {
		opts = append(opts, awscfg.WithRegion(region))
	}
	cfg, err := awscfg.LoadDefaultConfig(ctx, opts...)
	if err != nil {
		return nil, err
	}
	if cfg.Region == "" {
		return nil, fmt.Errorf("no AWS region: set AWS_S3_REGION")
	}
	return &S3{
		client: s3.NewFromConfig(cfg),
		bucket: bucket,
		cdn:    trimSlash(os.Getenv("AWS_CLOUDFRONT_DOMAIN")),
	}, nil
}

func (s *S3) Name() string { return "s3://" + s.bucket }

func (s *S3) Put(ctx context.Context, up Upload) (Result, error) {
	if limit := Limit(up.Kind); up.Size > limit {
		return Result{}, ErrTooLarge{Kind: up.Kind, Limit: limit}
	}
	name, err := randomName(up.Ext)
	if err != nil {
		return Result{}, err
	}
	key := keyFor(up, name)

	// The SDK needs a seekable body to sign, or an explicit length. Size comes
	// from the multipart header, and the reader is capped to it so a lying
	// Content-Length cannot stream an unbounded body into the bucket.
	body := io.LimitReader(up.Body, up.Size)
	if _, err := s.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:        aws.String(s.bucket),
		Key:           aws.String(key),
		Body:          body,
		ContentType:   aws.String(up.ContentType),
		ContentLength: aws.Int64(up.Size),
	}); err != nil {
		return Result{}, err
	}
	return Result{URL: s.urlFor(key), Key: key, Size: up.Size}, nil
}

// urlFor matches Java's publicUrl - https://{bucket}.s3.amazonaws.com/{key} -
// unless a CDN domain is configured, in which case that fronts the bucket.
func (s *S3) urlFor(key string) string {
	if s.cdn != "" {
		return s.cdn + "/" + key
	}
	return "https://" + s.bucket + ".s3.amazonaws.com/" + key
}

// Delete removes the object a stored URL points at. A URL this store did not
// write is ignored rather than treated as an error: media rows predating S3
// hold local /uploads/ links, and deleting such a row must still succeed.
func (s *S3) Delete(ctx context.Context, url string) error {
	key := s.keyFromURL(url)
	if key == "" {
		return nil
	}
	_, err := s.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	})
	return err
}

func (s *S3) keyFromURL(url string) string {
	for _, prefix := range s.prefixes() {
		if strings.HasPrefix(url, prefix) {
			return strings.TrimPrefix(url, prefix)
		}
	}
	return ""
}

// prefixes are every base this store might have written, so an object still
// resolves after the bucket gains or loses a CDN domain.
func (s *S3) prefixes() []string {
	out := []string{
		"https://" + s.bucket + ".s3.amazonaws.com/",
		"https://" + s.bucket + ".s3." + os.Getenv("AWS_S3_REGION") + ".amazonaws.com/",
	}
	if s.cdn != "" {
		out = append(out, s.cdn+"/")
	}
	return out
}

// randomName is the stored filename: never the caller's, which can contain path
// separators and would also let one upload overwrite another.
func randomName(ext string) (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf) + ext, nil
}
