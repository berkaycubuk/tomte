package tomte_test

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/berkaycubuk/tomte"
	"github.com/berkaycubuk/tomte/channels/telegram"
)

type overdueArgs struct {
	Days int `json:"days" jsonschema:"minimum age in days"`
}

type invoice struct {
	Number   string  `json:"number"`
	Customer string  `json:"customer"`
	Amount   float64 `json:"amount"`
}

func findOverdue(ctx context.Context, in overdueArgs) ([]invoice, error) {
	return []invoice{{"2026-041", "Acme", 1200}}, nil
}

// An agent that chases unpaid invoices every weekday morning and tells a
// Telegram chat what it did.
func ExampleNew() {
	tomte.New("invoice-chaser",
		tomte.Instructions("You chase unpaid invoices. List the overdue ones and say who to remind."),
		tomte.Model("https://openrouter.ai/api/v1", "z-ai/glm-4.6", os.Getenv("MODEL_KEY")),
		tomte.Tool("find_overdue", "List unpaid invoices older than N days", findOverdue),
		tomte.Cron("0 9 * * 1-5", "Chase invoices overdue by 14 days or more."),
		tomte.SendTo(telegram.New(os.Getenv("TELEGRAM_TOKEN"), os.Getenv("TELEGRAM_CHAT"))),
		tomte.Price(0.60, 2.20),
		tomte.MaxCost(0.50, 5.00),
	).Run()
}

// A tool's input is a struct: its fields are the arguments the model
// fills in, required unless omitempty.
func ExampleTool() {
	type args struct {
		City  string `json:"city" jsonschema:"the city to look up"`
		Units string `json:"units,omitempty" jsonschema:"metric or imperial; metric if not given"`
	}
	weather := func(ctx context.Context, in args) (string, error) {
		return fmt.Sprintf("12°C and raining in %s", in.City), nil
	}
	_ = tomte.Tool("weather", "The weather in a city now", weather)
}

// A job is Go code on a schedule, with no model. It replies NOTHING when
// there's nothing to say, and nothing is sent.
func ExampleJob() {
	diskCheck := func(ctx context.Context) (string, error) {
		free := 42 // percent, from syscall.Statfs in a real agent
		if free > 10 {
			return "NOTHING", nil
		}
		return fmt.Sprintf("Disk is %d%% free.", free), nil
	}
	tomte.New("disk-watch",
		tomte.Job("@hourly", "disk", diskCheck),
		tomte.SendTo(telegram.New(os.Getenv("TELEGRAM_TOKEN"), os.Getenv("TELEGRAM_CHAT"))),
	).Run()
}

// A command answers "/echo hello" written to the bot, with Go code.
func ExampleCommand() {
	echo := func(ctx context.Context, r tomte.Request) (string, error) {
		return strings.ToUpper(r.Args), nil
	}
	tomte.New("echo",
		tomte.Command("echo", "Say it back, louder", echo),
		tomte.SendTo(telegram.New(os.Getenv("TELEGRAM_TOKEN"), os.Getenv("TELEGRAM_CHAT"))),
	).Run()
}
