package main

import (
	"bytes"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go/aws/credentials"
	v4 "github.com/aws/aws-sdk-go/aws/signer/v4"
	"github.com/stretchr/testify/assert"
)

// func TestMain(m *testing.M) {
// 	log.SetOutput(ioutil.Discard)
// }

func newTestProxy(t *testing.T) *Handler {
	thf := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, "Hello, client")
	})
	return newTestProxyWithHandler(t, &thf)
}

func newTestProxyWithHandler(t *testing.T, thf *http.HandlerFunc) *Handler {
	ts := httptest.NewServer(thf)
	tsURL, _ := url.Parse(ts.URL)

	h, err := NewAwsS3ReverseProxy(Options{
		Debug:                 true,
		AllowedSourceEndpoint: "foobar.example.com",
		AllowedSourceSubnet:   []string{"0.0.0.0/0"},
		AwsCredentials:        []string{"fooooooooooooooo,bar"},
		Region:                "eu-test-1",
		UpstreamInsecure:      true,
		UpstreamEndpoint:      tsURL.Host,
	})
	assert.Nil(t, err)
	return h
}

func signRequest(r *http.Request) {
	// delete headers to get clean signature
	r.Header.Del("accept-encoding")
	r.Header.Del("authorization")
	r.Header.Set("X-Amz-Date", "20060102T150405Z")
	r.URL.RawPath = r.URL.Path

	// compute the expected signature with valid credentials
	body := bytes.NewReader([]byte{})
	signTime, _ := time.Parse("20060102T150405Z", r.Header["X-Amz-Date"][0])
	signer := v4.NewSigner(credentials.NewStaticCredentialsFromCreds(credentials.Value{
		AccessKeyID:     "fooooooooooooooo",
		SecretAccessKey: "bar",
	}))
	signer.Sign(r, body, "s3", "eu-test-1", signTime)
}

func verifySignature(w http.ResponseWriter, r *http.Request) {
	// save copy of the received signature
	receivedAuthorization := r.Header["Authorization"][0]

	// delete headers to get clean signature
	r.Header.Del("accept-encoding")
	r.Header.Del("authorization")

	// compute the expected signature with valid credentials
	body := bytes.NewReader([]byte{})
	signTime, _ := time.Parse("20060102T150405Z", r.Header["X-Amz-Date"][0])
	signer := v4.NewSigner(credentials.NewStaticCredentialsFromCreds(credentials.Value{
		AccessKeyID:     "fooooooooooooooo",
		SecretAccessKey: "bar",
	}))
	signer.Sign(r, body, "s3", "eu-test-1", signTime)
	expectedAuthorization := r.Header["Authorization"][0]

	// verify signature
	fmt.Fprintln(w, receivedAuthorization, expectedAuthorization)
	if receivedAuthorization == expectedAuthorization {
		fmt.Fprintln(w, "ok")
	} else {
		fmt.Fprintln(w, "failed signature check")
	}
}

func TestHandlerMissingAmzDate(t *testing.T) {
	h := newTestProxy(t)

	req := httptest.NewRequest(http.MethodGet, "http://foobar.example.com", nil)
	resp := httptest.NewRecorder()
	h.ServeHTTP(resp, req)
	assert.Equal(t, 400, resp.Code)
	assert.Contains(t, resp.Body.String(), "X-Amz-Date header missing or set multiple times")
}

func TestHandlerMissingAuthorization(t *testing.T) {
	h := newTestProxy(t)

	req := httptest.NewRequest(http.MethodGet, "http://foobar.example.com", nil)
	req.Header.Set("X-Amz-Date", "20060102T150405Z")
	resp := httptest.NewRecorder()
	h.ServeHTTP(resp, req)
	assert.Equal(t, 400, resp.Code)
	assert.Contains(t, resp.Body.String(), "Authorization header missing or set multiple times")
}

func TestHandlerMissingCredential(t *testing.T) {
	h := newTestProxy(t)

	req := httptest.NewRequest(http.MethodGet, "http://foobar.example.com", nil)
	req.Header.Set("X-Amz-Date", "20060102T150405Z")
	req.Header.Set("Authorization", "foobar")
	resp := httptest.NewRecorder()
	h.ServeHTTP(resp, req)
	assert.Equal(t, 400, resp.Code)
	assert.Contains(t, resp.Body.String(), "invalid Authorization header: Credential not found")
}

func TestHandlerInvalidSignature(t *testing.T) {
	h := newTestProxy(t)

	req := httptest.NewRequest(http.MethodGet, "http://foobar.example.com", nil)
	req.Header.Set("X-Amz-Date", "20060102T150405Z")
	req.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential=fooooooooooooooo/20190101/eu-test-1/s3/aws4_request, SignedHeaders=host;x-amz-content-sha256;x-amz-date, Signature=some-signature")
	resp := httptest.NewRecorder()
	h.ServeHTTP(resp, req)
	assert.Equal(t, 400, resp.Code)
	assert.Contains(t, resp.Body.String(), "invalid signature in Authorization header")
}

func TestHandlerValidSignature(t *testing.T) {
	h := newTestProxy(t)

	req := httptest.NewRequest(http.MethodGet, "http://foobar.example.com", nil)
	signRequest(req)
	resp := httptest.NewRecorder()
	h.ServeHTTP(resp, req)
	assert.Equal(t, 200, resp.Code)
	assert.Contains(t, resp.Body.String(), "Hello, client")
}

func TestHandlerInvalidCredential(t *testing.T) {
	h := newTestProxy(t)

	req := httptest.NewRequest(http.MethodGet, "http://foobar.example.com", nil)
	req.Header.Set("X-Amz-Date", "20060102T150405Z")
	req.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential=XXXooooooooooooo/20060102/eu-test-1/s3/aws4_request, SignedHeaders=host;x-amz-content-sha256;x-amz-date, Signature=a0d5e0c0924c1f9298c5f2a3925e202657bf1e239a1d6856235cbe0702855334") // signature computed manually for this test case
	resp := httptest.NewRecorder()
	h.ServeHTTP(resp, req)
	assert.Equal(t, 400, resp.Code)
	assert.Contains(t, resp.Body.String(), "invalid AccessKeyID in Credential")
}

func TestHandlerInvalidSourceSubnet(t *testing.T) {
	h := newTestProxy(t)
	_, newNet, _ := net.ParseCIDR("172.27.42.0/24")
	h.AllowedSourceSubnet = []*net.IPNet{newNet}

	req := httptest.NewRequest(http.MethodGet, "http://foobar.example.com", nil)
	req.Header.Set("X-Amz-Date", "20060102T150405Z")
	req.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential=XXXooooooooooooo/20060102/eu-test-1/s3/aws4_request, SignedHeaders=host;x-amz-content-sha256;x-amz-date, Signature=a0d5e0c0924c1f9298c5f2a3925e202657bf1e239a1d6856235cbe0702855334") // signature computed manually for this test case
	resp := httptest.NewRecorder()
	h.ServeHTTP(resp, req)
	assert.Equal(t, 400, resp.Code)
	assert.Contains(t, resp.Body.String(), "source IP not allowed")
}

func TestHandlerInvalidAmzDate(t *testing.T) {
	h := newTestProxy(t)

	req := httptest.NewRequest(http.MethodGet, "http://foobar.example.com", nil)
	req.Header.Set("X-Amz-Date", "foobar")
	req.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential=fooooooooooooooo/20060102/eu-test-1/s3/aws4_request, SignedHeaders=host;x-amz-content-sha256;x-amz-date, Signature=a0d5e0c0924c1f9298c5f2a3925e202657bf1e239a1d6856235cbe0702855334") // signature computed manually for this test case
	resp := httptest.NewRecorder()
	h.ServeHTTP(resp, req)
	assert.Equal(t, 400, resp.Code)
	assert.Contains(t, resp.Body.String(), "error parsing X-Amz-Date foobar")
}

func TestHandlerRawPathEncodingMatchingSignature(t *testing.T) {
	thf := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		verifySignature(w, r)
	})
	h := newTestProxyWithHandler(t, &thf)

	urls := []string{
		"http://foobar.example.com/foo%3Dbar/test.txt",
		"http://foobar.example.com/foo=bar/test.txt",
		"http://foobar.example.com/foo%3Dbar/test.txt?marker=1000",
		"http://foobar.example.com/foo=bar/test.txt?marker=1000",
	}

	for _, url := range urls {
		req := httptest.NewRequest(http.MethodGet, url, nil)
		signRequest(req)
		resp := httptest.NewRecorder()
		h.ServeHTTP(resp, req)
		assert.Equal(t, 200, resp.Code)
		assert.Contains(t, strings.TrimSpace(resp.Body.String()), "ok")
	}
}

func TestHandlerWithQueryArgs(t *testing.T) {
	thf := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		verifySignature(w, r)
		if r.URL.Query().Get("marker") == "1000" {
			fmt.Fprintln(w, "marker-ok")
		} else {
			fmt.Fprintln(w, "marker missing")
		}
	})
	h := newTestProxyWithHandler(t, &thf)

	urls := []string{
		"http://foobar.example.com/foo%3Dbar/test.txt?marker=1000",
		"http://foobar.example.com/foo=bar/test.txt?marker=1000",
	}

	for _, url := range urls {
		req := httptest.NewRequest(http.MethodGet, url, nil)
		signRequest(req)
		resp := httptest.NewRecorder()
		h.ServeHTTP(resp, req)
		assert.Equal(t, 200, resp.Code)
		assert.Contains(t, strings.TrimSpace(resp.Body.String()), "marker-ok")
	}
}

func TestHandlerPassCustomHeaders(t *testing.T) {
	thf := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-aws-s3-reverse-proxy") == "testing" {
			fmt.Fprintln(w, "ok")
		} else {
			fmt.Fprintln(w, "header missing")
		}
	})
	h := newTestProxyWithHandler(t, &thf)

	req := httptest.NewRequest(http.MethodGet, "http://foobar.example.com", nil)
	signRequest(req)
	req.Header.Set("x-aws-s3-reverse-proxy", "testing")
	resp := httptest.NewRecorder()
	h.ServeHTTP(resp, req)
	assert.Equal(t, 200, resp.Code)
	assert.Contains(t, strings.TrimSpace(resp.Body.String()), "ok")
}

func TestCheckGetObjectUrl(t *testing.T) {
	type testSheet struct {
		url  string
		isOk bool
	}

	urls := []testSheet{
		{"http://s3.amazon.com/bucket/key", true},
		{"http://s3.amazon.com/bucket/path/key", true},
		{"http://s3.amazon.com/bucket/path/key?list-type=2", false},
		{"http://s3.amazon.com/bucket/?list-type=2", false},
		{"http://s3.amazon.com/bucket/key?PartNumber=PartNumber&VersionId=VersionId", false},
	}

	for _, ur := range urls {
		u, err := url.Parse(ur.url)
		if err != nil {
			assert.Error(t, err)
		}
		checkResult := checkIfGetObjectUrl(u)
		assert.Equal(t, ur.isOk, checkResult)
	}

}
