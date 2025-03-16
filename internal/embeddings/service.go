package embeddings

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

type OpenAIRequest struct {
	Input string `json:"input"`
	Model string `json:"model"`
}

type OpenAIResponse struct {
	Data []struct {
		Embedding []float32 `json:"embedding"`
	} `json:"data"`
}

// Service handles interactions with the OpenAI embeddings API
type Service struct {
	apiKey string
}

// NewService creates a new embeddings service
func NewService(apiKey string) *Service {
	return &Service{
		apiKey: apiKey,
	}
}

// GetEmbedding generates an embedding for the given text
func (s *Service) GetEmbedding(text string) ([]float32, error) {
	apiURL := "https://api.openai.com/v1/embeddings"
	payload := OpenAIRequest{Input: text, Model: "text-embedding-ada-002"}
	jsonData, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %v", err)
	}

	req, err := http.NewRequest("POST", apiURL, bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %v", err)
	}

	req.Header.Set("Authorization", "Bearer "+s.apiKey)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to make request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("API request failed with status %d: %s", resp.StatusCode, string(body))
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %v", err)
	}

	var openAIResp OpenAIResponse
	if err := json.Unmarshal(body, &openAIResp); err != nil {
		return nil, fmt.Errorf("failed to parse response: %v", err)
	}

	if len(openAIResp.Data) == 0 {
		return nil, fmt.Errorf("no embedding data in response")
	}

	return openAIResp.Data[0].Embedding, nil
}

// ValidateAPIKey checks if the API key is valid by making a test request
func (s *Service) ValidateAPIKey() error {
	_, err := s.GetEmbedding("test")
	return err
}
