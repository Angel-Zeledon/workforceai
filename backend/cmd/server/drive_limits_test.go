package main

import (
	"testing"

	"aiworkforce/backend/internal/infrastructure/postgres"
	"aiworkforce/backend/internal/objstore"
)

func TestDriveUploadLimits(t *testing.T) {
	s3, err := objstore.NewS3(objstore.S3Config{Endpoint: "http://objects:9000", Bucket: "b", AccessKey: "a", SecretKey: "s"})
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("DRIVE_MAX_UPLOAD_MB", "")
	if l := driveLimits(&objstore.Tiered{Primary: s3, Fallback: &postgres.ObjectStore{}}); l.MaxUploadBytes != 500<<20 {
		t.Fatalf("S3 default: %d", l.MaxUploadBytes)
	}
	if l := driveLimits(&postgres.ObjectStore{}); l.MaxUploadBytes != 0 { // the drive raises 0 to MaxFileBytes (25 MB)
		t.Fatalf("Postgres default: %d", l.MaxUploadBytes)
	}
	t.Setenv("DRIVE_MAX_UPLOAD_MB", "2048")
	if l := driveLimits(s3); l.MaxUploadBytes != 2048<<20 {
		t.Fatalf("S3 configured: %d", l.MaxUploadBytes)
	}
	if l := driveLimits(&postgres.ObjectStore{}); l.MaxUploadBytes != 64<<20 {
		t.Fatalf("Postgres keeps at most 64 MB per object: %d", l.MaxUploadBytes)
	}
}
