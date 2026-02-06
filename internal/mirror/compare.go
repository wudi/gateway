package mirror

import (
	"crypto/sha256"
	"io"
	"net/http"
)

// PrimaryResponse holds the captured primary response data for comparison.
type PrimaryResponse struct {
	StatusCode int
	BodyHash   [32]byte
}

// CompareResult holds the result of comparing primary and mirror responses.
type CompareResult struct {
	StatusMatch bool `json:"status_match"`
	BodyMatch   bool `json:"body_match"`
}

// CompareMirrorResponse compares a mirror response against the primary response.
func CompareMirrorResponse(primary *PrimaryResponse, mirrorResp *http.Response) CompareResult {
	result := CompareResult{
		StatusMatch: primary.StatusCode == mirrorResp.StatusCode,
	}

	// Hash the mirror response body
	h := sha256.New()
	io.Copy(h, mirrorResp.Body)
	var mirrorHash [32]byte
	copy(mirrorHash[:], h.Sum(nil))
	result.BodyMatch = primary.BodyHash == mirrorHash

	return result
}
