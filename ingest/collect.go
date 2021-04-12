// Package ingest helps to send measurement data to edgeimpulse for processing
// into a model.
package ingest

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/md5"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"strings"
	"time"
)

// IngestionBaseURL is the default URL for uploading data.
var IngestionBaseURL = "https://ingestion.edgeimpulse.com"

// Sensor is a sensor for which values must be sent.
type Sensor struct {
	Name  string `json:"name"`
	Units string `json:"units"`
}

// CollectPayload is data to upload to EdgeImpulse for processing.
type CollectPayload struct {
	DeviceName string      `json:"device_name,omitempty"` // Optional.
	DeviceType string      `json:"device_type"`
	IntervalMS int64       `json:"interval_ms"`
	Sensors    []Sensor    `json:"sensors"` // All sensors in this payload.
	Values     [][]float64 `json:"values"`  // Values for each sensor. Each slice in Values must correspond to Sensors.
}

// AddData adds one set of measurements. One value for each sensor.
func (p *CollectPayload) AddData(data []float64) error {
	if len(p.Sensors) != len(data) {
		return fmt.Errorf("invalid data, got %d values, expect value for each of %d sensors", len(data), len(p.Sensors))
	}
	p.Values = append(p.Values, data)
	return nil
}

type protected struct {
	Version   string `json:"ver"`
	Algorithm string `json:"alg"`
	IAT       int64  `json:"iat,omitempty"`
}

type collectData struct {
	Protected protected      `json:"protected"`
	Signature string         `json:"signature"`
	Payload   CollectPayload `json:"payload"`
}

// Collector holds account details like keys, and allows uploading payloads.
type Collector struct {
	HTTPClient       *http.Client
	IngestionBaseURL string

	hmacKey []byte
	apiKey  string
}

// NewCollector makes a new Collector.
// The collectors baseURL is set based on environment variable EI_HOST if set (by prepending "https://ingestion."),
// otherwise defaulting to IngestionBaseURL.
// If you need custom HTTP handling, e.g. for proxy settings, you can override the default HTTPClient.
func NewCollector(apiKey, hmacKey string) (*Collector, error) {
	hmacKeyBuf, err := hex.DecodeString(hmacKey)
	if err != nil {
		return nil, fmt.Errorf("parsing hmac key: %v", err)
	}
	baseURL := IngestionBaseURL
	host := os.Getenv("EI_HOST")
	if host == "localhost" {
		baseURL = "http://localhost:4810"
	} else if strings.HasSuffix(host, "test.edgeimpulse.com") {
		baseURL = "http://ingestion." + host
	} else if strings.HasSuffix(host, "edgeimpulse.com") {
		baseURL = "https://ingestion." + host
	}
	c := &Collector{http.DefaultClient, baseURL, hmacKeyBuf, apiKey}
	return c, nil
}

// UploadOpts holds payload upload options.
type UploadOpts struct {
	Label              string
	DisallowDuplicates bool
}

// Upload sends the payload data to EdgeImpulse for ingestion.
// Upload returns the name of the sample as stored in EdgeImpulse Studio.
// For HTTP-related errors, the (wrapped) underlying errors from net/http or an HTTPError can be returned.
func (c *Collector) Upload(ctx context.Context, filename string, category string, payload CollectPayload, opts *UploadOpts) (string, error) {
	switch category {
	case "split", "training", "testing":
		break
	default:
		return "", fmt.Errorf("invalid category %q, need one of: split, training, testing", category)
	}

	// Prepare data, insert zeros for signature, then marshal data to JSON.
	data := collectData{
		Protected: protected{
			Version:   "v1",
			Algorithm: "HS256",
			IAT:       time.Now().Unix(),
		},
		Signature: fmt.Sprintf("%x", make([]byte, 32)),
		Payload:   payload,
	}
	buf, err := json.Marshal(data)
	if err != nil {
		return "", fmt.Errorf("marshal data to JSON: %v", err)
	}

	// Now actually sign the data (that has the zero signature).
	h := hmac.New(sha256.New, c.hmacKey)
	h.Write(buf)
	actualSig := fmt.Sprintf("%x", h.Sum(nil))

	// Replace the zero signature with the actual signature.
	i := bytes.Index(buf, []byte(data.Signature))
	if i < 0 {
		return "", fmt.Errorf("internal error: could not find zero signature")
	}
	copy(buf[i:], []byte(actualSig))

	if category == "split" {
		pbuf, err := json.Marshal(payload)
		if err != nil {
			return "", fmt.Errorf("marshal payload: %v", err)
		}
		h := fmt.Sprintf("%x", md5.Sum(pbuf))
		for _, b := range h {
			if b == 'f' {
				continue
			} else if b >= '0' && b <= '9' || b == 'a' || b == 'b' {
				category = "training"
			} else if b == 'c' || b == 'd' || b == 'e' {
				category = "testing"
			} else {
				return "", fmt.Errorf("internal error: cannot determine category for split, byte %v", b)
			}
			break
		}
	}

	// Prepare HTTP request for sending data.
	url := fmt.Sprintf("%s/api/%s/data", c.IngestionBaseURL, category)
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(buf))
	if err != nil {
		return "", fmt.Errorf("new HTTP request: %v", err)
	}
	req.Header.Add("x-api-key", c.apiKey)
	req.Header.Add("x-file-name", filename)
	req.Header.Add("Content-Type", "application/json")
	if opts != nil && opts.Label != "" {
		req.Header.Add("x-label", opts.Label)
	}
	if opts != nil && opts.DisallowDuplicates {
		req.Header.Add("x-disallow-duplicates", "1")
	}

	// Perform HTTP request, and handle the response, including possible errors.
	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		// Attempt to read a response message to use in error message, otherwise use http status message.
		msg := resp.Status
		buf, err := ioutil.ReadAll(resp.Body)
		if err == nil && len(buf) > 0 {
			msg = string(buf)
		}
		return "", HTTPError{resp.StatusCode, msg}
	}
	respBuf, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("reading response message: %w", err)
	}
	return string(respBuf), nil
}

// HTTPError represents an HTTP error code and message.
type HTTPError struct {
	Code   int    // HTTP status code, eg 401 or 500.
	Status string // Status message, either from body or the HTTP response status line.
}

// Error returns a human-readable description of the HTTP error.
func (e HTTPError) Error() string {
	return fmt.Sprintf("http response error, code %d: %s", e.Code, e.Status)
}

// Ensure HTTPError implements the error interface.
var _ error = HTTPError{}
