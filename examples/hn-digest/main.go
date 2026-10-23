// HN digest sends a short morning digest of the Hacker News stories that
// match your interests to Telegram.
//
//	MODEL_URL=https://openrouter.ai/api/v1 MODEL=z-ai/glm-4.6 MODEL_KEY=… \
//	TELEGRAM_TOKEN=… TELEGRAM_CHAT=… INTERESTS="Go, SQLite, self-hosting" \
//		go run ./examples/hn-digest run "send the digest"
//
// Without TELEGRAM_TOKEN the digest is printed. Run it with no arguments
// to send one every weekday at 08:00.
package main

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/berkaycubuk/tomte"
	"github.com/berkaycubuk/tomte/channels/telegram"
)

//go:embed instructions.md
var instructions string

func main() {
	interests := os.Getenv("INTERESTS")
	if interests == "" {
		interests = "programming, open source, startups"
	}
	opts := []tomte.Option{
		tomte.Instructions(strings.ReplaceAll(instructions, "{{interests}}", interests)),
		tomte.Model(os.Getenv("MODEL_URL"), os.Getenv("MODEL"), os.Getenv("MODEL_KEY")),
		tomte.Tool("top_stories", "The current top Hacker News stories, best first", topStories),
		tomte.Cron("0 8 * * 1-5", "Send this morning's digest."),
		tomte.Price(0.60, 2.20), // z-ai/glm-4.6 on OpenRouter; change it with the model
		tomte.MaxCost(0.05, 0.25),
		tomte.MaxSteps(5),
	}
	if token := os.Getenv("TELEGRAM_TOKEN"); token != "" {
		opts = append(opts, tomte.SendTo(telegram.New(token, os.Getenv("TELEGRAM_CHAT"))))
	}
	tomte.New("hn-digest", opts...).Run()
}

type storiesArgs struct {
	Limit int `json:"limit,omitempty" jsonschema:"how many stories, 1 to 60; 30 if not given"`
}

type story struct {
	Title    string `json:"title"`
	URL      string `json:"url"`
	Score    int    `json:"score"`
	Comments int    `json:"comments"`
	Age      string `json:"age"`
}

var client = &http.Client{Timeout: 20 * time.Second}

func topStories(ctx context.Context, in storiesArgs) ([]story, error) {
	limit := in.Limit
	if limit < 1 || limit > 60 {
		limit = 30
	}
	var ids []int
	if err := getJSON(ctx, "https://hacker-news.firebaseio.com/v0/topstories.json", &ids); err != nil {
		return nil, err
	}
	ids = ids[:min(limit, len(ids))]

	// The API has one request per story; fetch them eight at a time.
	stories := make([]*story, len(ids))
	sem := make(chan struct{}, 8)
	var wg sync.WaitGroup
	for i, id := range ids {
		wg.Go(func() {
			sem <- struct{}{}
			defer func() { <-sem }()
			var item struct {
				Title       string `json:"title"`
				URL         string `json:"url"`
				Score       int    `json:"score"`
				Descendants int    `json:"descendants"`
				Time        int64  `json:"time"`
			}
			if getJSON(ctx, fmt.Sprintf("https://hacker-news.firebaseio.com/v0/item/%d.json", id), &item) != nil {
				return
			}
			if item.URL == "" { // Ask HN and the like link to the discussion
				item.URL = fmt.Sprintf("https://news.ycombinator.com/item?id=%d", id)
			}
			stories[i] = &story{
				Title:    item.Title,
				URL:      item.URL,
				Score:    item.Score,
				Comments: item.Descendants,
				Age:      fmt.Sprintf("%d hours", int(time.Since(time.Unix(item.Time, 0)).Hours())),
			}
		})
	}
	wg.Wait()

	var out []story
	for _, s := range stories {
		if s != nil {
			out = append(out, *s)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("couldn't fetch any of the %d stories", len(ids))
	}
	return out, nil
}

func getJSON(ctx context.Context, url string, v any) error {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("%s: %s", url, resp.Status)
	}
	return json.NewDecoder(resp.Body).Decode(v)
}
