// Dice is the smallest useful tomte: it rolls dice when asked.
//
//	MODEL_URL=https://openrouter.ai/api/v1 MODEL=z-ai/glm-4.6 MODEL_KEY=… \
//		go run ./examples/dice run "roll two d20 and tell me the total"
package main

import (
	"context"
	"fmt"
	"math/rand/v2"
	"os"

	"github.com/berkaycubuk/tomte"
)

type rollArgs struct {
	Dice  int `json:"dice" jsonschema:"how many dice to roll, 1 to 100"`
	Sides int `json:"sides" jsonschema:"sides on each die, 2 to 1000"`
}

func roll(_ context.Context, in rollArgs) ([]int, error) {
	if in.Dice < 1 || in.Dice > 100 || in.Sides < 2 || in.Sides > 1000 {
		return nil, fmt.Errorf("can't roll %d d%d", in.Dice, in.Sides)
	}
	rolls := make([]int, in.Dice)
	for i := range rolls {
		rolls[i] = 1 + rand.IntN(in.Sides)
	}
	return rolls, nil
}

func main() {
	tomte.New("dice",
		tomte.Instructions("You roll dice for tabletop games. Always use the roll tool; never make up a roll."),
		tomte.Model(os.Getenv("MODEL_URL"), os.Getenv("MODEL"), os.Getenv("MODEL_KEY")),
		tomte.Tool("roll", "Roll dice and get each result", roll),
	).Run()
}
