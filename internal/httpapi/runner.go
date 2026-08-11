package httpapi

import (
	"context"
	"errors"

	"github.com/smallnest/pigo/internal/httpapi/gen"
)

func unavailableRunner(context.Context, string, string) (gen.PromptResponse, error) {
	return gen.PromptResponse{}, errors.New("prompt runner is not configured")
}
