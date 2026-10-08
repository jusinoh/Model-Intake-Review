// Command model-intake vets Hugging Face model repositories before anyone
// downloads or loads them.
//
// v0.1, piece 1: make one request to the Hugging Face model info endpoint and
// report what came back. Later pieces decode the response, list the files,
// classify them, and read the model name from the command line.
package main

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
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
	endpoint := hubAPI + "/models/" + modelID

	client := &http.Client{Timeout: requestTimeout}

	req, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		return fmt.Errorf("building request: %w", err)
	}
	req.Header.Set("User-Agent", userAgent)

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("requesting %s: %w", endpoint, err)
	}
	defer resp.Body.Close()

	// Go does not treat 404 or 500 as an error. We have to check.
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status from %s: %s", endpoint, resp.Status)
	}

	// Read one byte more than the cap. If we get it, the response was too big,
	// and we refuse it rather than silently working with a cut-off copy.
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes+1))
	if err != nil {
		return fmt.Errorf("reading response from %s: %w", endpoint, err)
	}
	if len(body) > maxBodyBytes {
		return fmt.Errorf("response from %s is larger than %d bytes, refusing it", endpoint, maxBodyBytes)
	}

	fmt.Printf("GET %s\n", endpoint)
	fmt.Printf("status: %s\n", resp.Status)
	fmt.Printf("received %d bytes\n", len(body))
	return nil
}