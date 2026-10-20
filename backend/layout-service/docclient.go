package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
)

// DocClient fetches a document's content from the Document Service. Events only carry ids, so the content
// never travels through RabbitMQ (claim check pattern).
type DocClient struct {
	BaseURL string
	HTTP    *http.Client
}

func (c *DocClient) FetchText(ctx context.Context, documentID string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		c.BaseURL+"/documents/"+url.PathEscape(documentID)+"/content", nil)
	if err != nil {
		return "", err
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("document service answered %d for document %s", resp.StatusCode, documentID)
	}
	var body struct {
		Text *string `json:"text"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", err
	}
	if body.Text == nil {
		return "", errors.New("document has no text yet")
	}
	return *body.Text, nil
}
