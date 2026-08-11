package main

import (
	"context"
	"fmt"
	"time"

	"github.com/smallnest/pigo/internal/acp"
	"github.com/smallnest/pigo/internal/agentcore"
	"github.com/smallnest/pigo/internal/cli/config"
	"github.com/smallnest/pigo/internal/cli/run"
	"github.com/smallnest/pigo/internal/httpapi"
	"github.com/smallnest/pigo/internal/httpapi/gen"
)

func makePromptRunner(opts cliOptions) (httpapi.PromptRunner, error) {
	env, err := run.SetupEnv(
		opts.model,
		opts.baseURL,
		opts.protocol,
		opts.provider,
		opts.apiKey,
		opts.noTools,
		opts.noSkills,
		opts.systemPrompt,
		opts.appendSystemPrompt,
		opts.memory.Memory.Enabled,
		run.NewToolPolicy(opts.allowedTools, opts.disallowedTools),
	)
	if err != nil {
		return nil, err
	}
	models := acp.NewConfiguredModels(config.FileConfigPath())
	_ = models.Load()
	thinking := agentcore.ThinkingMedium
	if opts.thinkingLevel != "" {
		thinking = agentcore.ThinkingLevel(opts.thinkingLevel)
	}
	runner := &acp.RuntimeRunner{
		Provider:         env.Provider,
		ProviderName:     env.ProviderName,
		Model:            env.Model,
		APIKey:           env.APIKey,
		ThinkingLevel:    thinking,
		Tools:            env.Tools,
		ConfiguredModels: models,
	}
	return func(ctx context.Context, _, text string) (gen.PromptResponse, error) {
		_, last, err := runner.Run(ctx, text, nil, nil, env.SysPrompt, env.Model, "", nil, nil, acp.TurnHooks{})
		if err != nil {
			return gen.PromptResponse{}, err
		}
		reply := ""
		if last != nil {
			reply = agentcore.ContentToText(last.Content)
		}
		return gen.PromptResponse{
			MessageId:  fmt.Sprintf("msg-%d", time.Now().UnixNano()),
			StopReason: "end_turn",
			Text:       &reply,
		}, nil
	}, nil
}
