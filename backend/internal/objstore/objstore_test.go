package objstore

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestOrgKey(t *testing.T) {
	k := OrgKey("o1", "drive", "f")
	if k != "orgs/o1/drive/f" || OrgOf(k) != "o1" || OrgOf("x/y/z") != "" {
		t.Fatal(k)
	}
}

// The published AWS example (Signature V4, "GET Object" with a Range header).
func TestSigV4AWSVector(t *testing.T) {
	req, _ := http.NewRequest("GET", "https://examplebucket.s3.amazonaws.com/test.txt", nil)
	when := time.Date(2013, 5, 24, 0, 0, 0, 0, time.UTC)
	signV4(req, "examplebucket.s3.amazonaws.com", map[string]string{"range": "bytes=0-9"}, emptySHA,
		"AKIAIOSFODNN7EXAMPLE", "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY", "us-east-1", when)
	got := req.Header.Get("Authorization")
	want := "AWS4-HMAC-SHA256 Credential=AKIAIOSFODNN7EXAMPLE/20130524/us-east-1/s3/aws4_request,SignedHeaders=host;range;x-amz-content-sha256;x-amz-date,Signature=f0e8bdb87c964420e857bd35b5d6ed310bd44f0170aba48dd91039c6036bdb41"
	if strings.ReplaceAll(got, ", ", ",") != want {
		t.Fatalf("signature mismatch:\n got %s\nwant %s", got, want)
	}
}

func TestNewS3Validation(t *testing.T) {
	for _, c := range []S3Config{{}, {Endpoint: "ftp://x", Bucket: "b", AccessKey: "a", SecretKey: "s"}, {Endpoint: "http://x"}} {
		if _, err := NewS3(c); err == nil {
			t.Fatalf("%+v accepted", c)
		}
	}
}
