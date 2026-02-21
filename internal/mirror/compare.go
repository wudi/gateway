package mirror

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"

	"github.com/tidwall/gjson"
)

// PrimaryResponse holds the captured primary response data for comparison.
type PrimaryResponse struct {
	StatusCode int
	BodyHash   [32]byte
}

// PrimaryDiffResponse holds the captured primary response data for detailed diff comparison.
type PrimaryDiffResponse struct {
	StatusCode int
	Headers    http.Header
	Body       []byte
	BodyHash   [32]byte
	Truncated  bool
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

// CompareMirrorResponseDetailed compares a mirror response against the primary with detailed diffs.
// The mirror response body is read and closed by this function.
func CompareMirrorResponseDetailed(primary *PrimaryDiffResponse, mirrorResp *http.Response, dc *DiffConfig) (CompareResult, *DiffDetail) {
	detail := &DiffDetail{}
	result := CompareResult{StatusMatch: true, BodyMatch: true}

	// 1. Status comparison
	if primary.StatusCode != mirrorResp.StatusCode {
		result.StatusMatch = false
		detail.StatusDiff = &StatusDiff{
			PrimaryStatus: primary.StatusCode,
			MirrorStatus:  mirrorResp.StatusCode,
		}
	}

	// 2. Header comparison
	detail.HeaderDiffs = compareHeaders(primary.Headers, mirrorResp.Header, dc.ignoreHeaders)

	// 3. Body comparison — read mirror body (up to limit + hash)
	mirrorBody, mirrorHash, mirrorTruncated := readMirrorBody(mirrorResp.Body, dc.maxBodyCapture)

	if primary.Truncated || mirrorTruncated {
		// Either side truncated — fall back to hash comparison
		if primary.BodyHash != mirrorHash {
			result.BodyMatch = false
			detail.BodyDiffs = append(detail.BodyDiffs, BodyDiff{
				Type: "hash_mismatch",
				Details: map[string]interface{}{
					"primary_hash_prefix": fmt.Sprintf("%x", primary.BodyHash[:8]),
					"mirror_hash_prefix":  fmt.Sprintf("%x", mirrorHash[:8]),
					"truncated":           true,
				},
			})
		}
	} else {
		detail.BodyDiffs = compareBody(primary.Body, mirrorBody, primary.BodyHash, mirrorHash, dc.ignoreJSONFields)
		if len(detail.BodyDiffs) > 0 {
			result.BodyMatch = false
		}
	}

	return result, detail
}

func compareHeaders(primary, mirror http.Header, ignore map[string]bool) []HeaderDiff {
	var diffs []HeaderDiff
	seen := make(map[string]bool)

	for key := range primary {
		lk := strings.ToLower(key)
		if ignore[lk] {
			continue
		}
		seen[lk] = true
		pv := primary.Get(key)
		mv := mirror.Get(key)
		if pv != mv {
			diffs = append(diffs, HeaderDiff{
				Header:       key,
				PrimaryValue: pv,
				MirrorValue:  mv,
			})
		}
	}

	for key := range mirror {
		lk := strings.ToLower(key)
		if ignore[lk] || seen[lk] {
			continue
		}
		pv := primary.Get(key)
		mv := mirror.Get(key)
		if pv != mv {
			diffs = append(diffs, HeaderDiff{
				Header:       key,
				PrimaryValue: pv,
				MirrorValue:  mv,
			})
		}
	}

	// Sort for deterministic output
	sort.Slice(diffs, func(i, j int) bool {
		return diffs[i].Header < diffs[j].Header
	})

	return diffs
}

func readMirrorBody(body io.Reader, maxCapture int64) ([]byte, [32]byte, bool) {
	h := sha256.New()
	lr := io.LimitReader(body, maxCapture+1)
	tr := io.TeeReader(lr, h)

	data, _ := io.ReadAll(tr)
	truncated := int64(len(data)) > maxCapture

	// Continue hashing remainder if truncated
	if truncated {
		io.Copy(h, body)
		data = data[:maxCapture]
	}

	var hash [32]byte
	copy(hash[:], h.Sum(nil))
	return data, hash, truncated
}

func compareBody(primaryBody, mirrorBody []byte, primaryHash, mirrorHash [32]byte, ignoreFields []string) []BodyDiff {
	// Quick check: hashes match means bodies are identical
	if primaryHash == mirrorHash {
		return nil
	}

	// Check size difference
	if len(primaryBody) != len(mirrorBody) {
		sizeDiff := BodyDiff{
			Type: "size_diff",
			Details: map[string]interface{}{
				"primary_size": len(primaryBody),
				"mirror_size":  len(mirrorBody),
			},
		}
		// If either is empty, just report size diff
		if len(primaryBody) == 0 || len(mirrorBody) == 0 {
			return []BodyDiff{sizeDiff}
		}
		// Continue to check content, but include size diff
		diffs := []BodyDiff{sizeDiff}
		contentDiffs := compareBodyContent(primaryBody, mirrorBody, ignoreFields)
		return append(diffs, contentDiffs...)
	}

	return compareBodyContent(primaryBody, mirrorBody, ignoreFields)
}

func compareBodyContent(primaryBody, mirrorBody []byte, ignoreFields []string) []BodyDiff {
	// Try JSON comparison if both are valid JSON
	if gjson.ValidBytes(primaryBody) && gjson.ValidBytes(mirrorBody) {
		return compareJSON(primaryBody, mirrorBody, ignoreFields)
	}

	// Non-JSON: just report content differs
	return []BodyDiff{{
		Type: "content_diff",
		Details: map[string]interface{}{
			"primary_preview": truncateString(string(primaryBody), 200),
			"mirror_preview":  truncateString(string(mirrorBody), 200),
		},
	}}
}

func compareJSON(primaryBody, mirrorBody []byte, ignoreFields []string) []BodyDiff {
	ignoreSet := make(map[string]bool, len(ignoreFields))
	for _, f := range ignoreFields {
		ignoreSet[f] = true
	}

	// Parse both into maps for field-level comparison
	var primaryMap, mirrorMap map[string]interface{}
	if err := json.Unmarshal(primaryBody, &primaryMap); err != nil {
		// Not a JSON object — compare as raw JSON strings
		return compareJSONRaw(primaryBody, mirrorBody)
	}
	if err := json.Unmarshal(mirrorBody, &mirrorMap); err != nil {
		return compareJSONRaw(primaryBody, mirrorBody)
	}

	// Collect field-level diffs using gjson for consistency
	var diffs []BodyDiff
	allKeys := collectKeys(primaryMap, mirrorMap)

	for _, key := range allKeys {
		if ignoreSet[key] {
			continue
		}
		pResult := gjson.GetBytes(primaryBody, key)
		mResult := gjson.GetBytes(mirrorBody, key)

		if pResult.Raw != mResult.Raw {
			diffs = append(diffs, BodyDiff{
				Type: "json_field_diff",
				Details: map[string]interface{}{
					"field":         key,
					"primary_value": truncateString(pResult.Raw, 200),
					"mirror_value":  truncateString(mResult.Raw, 200),
				},
			})
		}
	}

	return diffs
}

func compareJSONRaw(primaryBody, mirrorBody []byte) []BodyDiff {
	return []BodyDiff{{
		Type: "content_diff",
		Details: map[string]interface{}{
			"primary_preview": truncateString(string(primaryBody), 200),
			"mirror_preview":  truncateString(string(mirrorBody), 200),
		},
	}}
}

func collectKeys(a, b map[string]interface{}) []string {
	keys := make(map[string]bool)
	for k := range a {
		keys[k] = true
	}
	for k := range b {
		keys[k] = true
	}
	sorted := make([]string, 0, len(keys))
	for k := range keys {
		sorted = append(sorted, k)
	}
	sort.Strings(sorted)
	return sorted
}

func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
