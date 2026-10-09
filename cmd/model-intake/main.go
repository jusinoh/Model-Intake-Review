// Command model-intake vets Hugging Face model repositories before anyone
// downloads or loads them.
//
// v0.1, piece 2: request the model info endpoint, decode the fields the tool
// needs into a struct, and print them. Later pieces list the files, classify
// them, and read the model name from the command line.
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"
	"unicode"
)

const (
	// hubAPI is the base URL for the Hugging Face Hub API.
	hubAPI = "https://huggingface.co/api"

	// requestTimeout bounds the whole request, including reading the body.
	// Without it, a server that stops responding would hang the scan forever.
	requestTimeout = 30 * time.Second

	// maxBodyBytes caps how much of a response we are willing to read (1 MiB).
	// A model info response is a few kilobytes, so anything bigger is suspect.
	maxBodyBytes = 1 << 20

	// userAgent identifies this tool to the server.
	userAgent = "model-intake/0.1-dev"

	// exitError is the exit code for tool errors.
	// The full set: 0 pass, 1 policy failure, 2 error.
	exitError = 2
)

// commitSHA matches a full 40-character Git commit ID.
// Compiled once when the program starts, not on every use.
var commitSHA = regexp.MustCompile(`^[0-9a-f]{40}$`)

// ModelInfo holds the fields model-intake uses from the model info endpoint.
// Fields in the response that are not listed here are ignored.
type ModelInfo struct {
	ID           string    `json:"id"`
	SHA          string    `json:"sha"`
	Author       string    `json:"author"`
	Gated        Gated     `json:"gated"`
	LastModified time.Time `json:"lastModified"`
	CardData     CardData  `json:"cardData"`
}

// CardData holds the fields we use from the model card's metadata.
// It is empty when a repo has no model card.
type CardData struct {
	License string `json:"license"`
}

// Gated describes whether a repo requires approval before download.
//
// The API sends a JSON boolean (false) for open repos and a string such as
// "manual" for gated ones, so a plain bool field cannot decode both.
type Gated struct {
	Enabled bool
	Mode    string // for example "manual" or "auto"; empty when not gated
}

// UnmarshalJSON lets Gated decode either a boolean or a string.
// encoding/json calls this method automatically because Gated has it.
func (g *Gated) UnmarshalJSON(data []byte) error {
	var b bool
	if err := json.Unmarshal(data, &b); err == nil {
		*g = Gated{Enabled: b}
		return nil
	}

	var s string
	if err := json.Unmarshal(data, &s); err == nil {
		// Any string means the repo is gated. An unfamiliar mode is still
		// treated as gated, so a surprise value fails closed.
		*g = Gated{Enabled: true, Mode: s}
		return nil
	}

	return fmt.Errorf("gated: expected a boolean or a string, got %.40q", data)
}

func main() {
	// Hardcoded for now. Piece 5 reads it from the command line and
	// validates it, because this value ends up inside a URL.
	modelID := "openai-community/gpt2"

	if err := run(modelID); err != nil {
		fmt.Fprintln(os.Stderr, "model-intake:", err)
		os.Exit(exitError)
	}
}

// run does the real work and returns an error instead of exiting.
// Keeping os.Exit in main means every defer inside run gets to run first.
func run(modelID string) error {
	info, err := fetchModelInfo(modelID)
	if err != nil {
		return err
	}

	fmt.Printf("%-14s %s\n", "repo:", clean(info.ID))
	fmt.Printf("%-14s %s\n", "commit:", info.SHA)
	fmt.Printf("%-14s %s\n", "author:", clean(info.Author))
	fmt.Printf("%-14s %s\n", "license:", describeLicense(info.CardData.License))
	fmt.Printf("%-14s %s\n", "gated:", describeGated(info.Gated))
	fmt.Printf("%-14s %s\n", "last modified:", describeTime(info.LastModified))
	return nil
}

// fetchModelInfo requests the model info endpoint and decodes the response.
func fetchModelInfo(modelID string) (ModelInfo, error) {
	endpoint := hubAPI + "/models/" + modelID

	body, err := get(endpoint)
	if err != nil {
		return ModelInfo{}, err
	}

	var info ModelInfo
	if err := json.Unmarshal(body, &info); err != nil {
		return ModelInfo{}, fmt.Errorf("decoding response from %s: %w", endpoint, err)
	}

	// The commit is what every later step pins to. If it is missing or
	// malformed, nothing downstream can be trusted, so stop here.
	if !commitSHA.MatchString(info.SHA) {
		return ModelInfo{}, fmt.Errorf("response from %s has no valid commit SHA (got %.50q)", endpoint, info.SHA)
	}

	return info, nil
}

// get performs a bounded GET request and returns the response body.
func get(endpoint string) ([]byte, error) {
	client := &http.Client{Timeout: requestTimeout}

	req, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("building request: %w", err)
	}
	req.Header.Set("User-Agent", userAgent)

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("requesting %s: %w", endpoint, err)
	}
	defer resp.Body.Close()

	// Go does not treat 404 or 500 as an error. We have to check.
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status from %s: %s", endpoint, resp.Status)
	}

	// Read one byte more than the cap. If we get it, the response was too big,
	// and we refuse it rather than silently working with a cut-off copy.
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes+1))
	if err != nil {
		return nil, fmt.Errorf("reading response from %s: %w", endpoint, err)
	}
	if len(body) > maxBodyBytes {
		return nil, fmt.Errorf("response from %s is larger than %d bytes, refusing it", endpoint, maxBodyBytes)
	}

	return body, nil
}

func describeLicense(license string) string {
	if license == "" {
		return "none declared"
	}
	return clean(license)
}

func describeGated(g Gated) string {
	if !g.Enabled {
		return "no"
	}
	if g.Mode == "" {
		return "yes"
	}
	return "yes (" + clean(g.Mode) + ")"
}

func describeTime(t time.Time) string {
	if t.IsZero() {
		return "unknown"
	}
	// Go formats dates by example: this layout means YYYY-MM-DD.
	return t.Format("2006-01-02")
}

// clean replaces control characters with "?" before printing.
//
// Text like the license comes from the model's publisher. A hostile value
// could carry terminal escape sequences that rewrite what you see on screen,
// so never print API text to a terminal unfiltered.
func clean(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsPrint(r) {
			return r
		}
		return '?'
	}, s)
}