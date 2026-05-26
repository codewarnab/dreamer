package copilotsdk

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	copilot "github.com/github/copilot-sdk/go"

	"dreamer/internal/analyzer"
	"dreamer/internal/chat"
	"dreamer/internal/errs"
	"dreamer/internal/fsutil"
)

const ID = "copilot-sdk"

type Options struct {
	CopilotHome        string
	UseLoggedInUser    bool
	UseLoggedInUserSet bool
	CLIURL             string
	AutoStart          bool
	Model              string
}

type sdkClient interface {
	Start(ctx context.Context) error
	CreateSession(ctx context.Context, config *copilot.SessionConfig) (sdkSession, error)
	Stop() error
}

type sdkSession interface {
	SendAndWait(ctx context.Context, options copilot.MessageOptions) (*copilot.SessionEvent, error)
	Disconnect() error
}

type sdkClientFactory func(options *copilot.ClientOptions) sdkClient

var newSDKClient sdkClientFactory = func(options *copilot.ClientOptions) sdkClient {
	return &sdkClientAdapter{client: copilot.NewClient(options)}
}

// SetSDKClientFactory installs a factory override for tests and returns a
// restore function. Not for production use.
func SetSDKClientFactory(factory sdkClientFactory) func() {
	previous := newSDKClient
	newSDKClient = factory
	return func() {
		newSDKClient = previous
	}
}

type provider struct {
	options Options
	client  sdkClient
	started bool
}

// New builds a Copilot SDK provider. The Provider is started lazily on first
// NewSession unless options.AutoStart is true.
func New(options Options) (analyzer.Provider, error) {
	sdkOptions, err := buildSDKClientOptions(options)
	if err != nil {
		return nil, err
	}
	p := &provider{
		options: options,
		client:  newSDKClient(sdkOptions),
	}
	if options.AutoStart {
		if err := p.Start(context.Background()); err != nil {
			return nil, err
		}
	}
	return p, nil
}

func (p *provider) ID() string { return ID }

// SupportsParallelSessions: SDK manages sessions independently.
func (p *provider) SupportsParallelSessions() bool { return true }

func (p *provider) Start(ctx context.Context) error {
	if ctx == nil {
		return analyzer.ErrNilContext
	}
	if p.started {
		return nil
	}
	if err := p.client.Start(ctx); err != nil {
		return errs.ProviderUnavailable(ID, "start",
			fmt.Errorf("unable to start Copilot SDK client; ensure Copilot CLI is installed (`npm install -g @github/copilot`) and authenticated (run `copilot` then `/login`): %w", err),
		)
	}
	p.started = true
	return nil
}

func (p *provider) NewSession(ctx context.Context, sessionConfig analyzer.SessionConfig) (analyzer.Session, error) {
	if ctx == nil {
		return nil, analyzer.ErrNilContext
	}
	if err := p.Start(ctx); err != nil {
		return nil, err
	}

	requestedModel := strings.TrimSpace(sessionConfig.Model)
	if requestedModel == "" {
		requestedModel = strings.TrimSpace(p.options.Model)
	}
	sdkSessionConfig := buildSessionConfig(requestedModel, sessionConfig)

	session, err := p.client.CreateSession(ctx, sdkSessionConfig)
	if err != nil {
		if requestedModel == "" {
			return nil, errs.ProviderUnavailable(ID, "session.new",
				fmt.Errorf("unable to create Copilot session; check auth and model availability: %w", err),
			)
		}
		fallback, fallbackErr := p.client.CreateSession(ctx, buildSessionConfig("", sessionConfig))
		if fallbackErr != nil {
			return nil, errs.ProviderUnavailable(ID, "session.new",
				fmt.Errorf("unable to create Copilot session with requested model %q (%v) and SDK auto-model fallback (%w)", requestedModel, err, fallbackErr),
			)
		}
		return &copilotSession{session: fallback, runID: sessionConfig.RunID}, nil
	}
	return &copilotSession{session: session, runID: sessionConfig.RunID}, nil
}

func (p *provider) Close() error {
	if err := p.client.Stop(); err != nil {
		return fmt.Errorf("close copilot-sdk provider: unable to stop Copilot SDK client cleanly: %w", err)
	}
	return nil
}

type copilotSession struct {
	session sdkSession
	runID   string
}

func (s *copilotSession) Run(ctx context.Context, prompt string, timeout time.Duration) (string, error) {
	if ctx == nil {
		return "", analyzer.ErrNilContext
	}
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	event, err := s.session.SendAndWait(ctx, copilot.MessageOptions{Prompt: chat.PrependMarker(prompt, s.runID)})
	if err != nil {
		return "", fmt.Errorf("run copilot-sdk prompt: Copilot request failed; verify authentication and connectivity: %w", err)
	}
	if event == nil || event.Data == nil {
		return "", fmt.Errorf("run copilot-sdk prompt: Copilot session completed without assistant output")
	}
	message, ok := event.Data.(*copilot.AssistantMessageData)
	if !ok {
		return "", fmt.Errorf("run copilot-sdk prompt: unexpected response type %T", event.Data)
	}
	return message.Content, nil
}

func (s *copilotSession) Close() error {
	if err := s.session.Disconnect(); err != nil {
		return fmt.Errorf("close copilot-sdk session: unable to disconnect Copilot session cleanly: %w", err)
	}
	return nil
}

type sdkClientAdapter struct{ client *copilot.Client }

func (c *sdkClientAdapter) Start(ctx context.Context) error { return c.client.Start(ctx) }

func (c *sdkClientAdapter) CreateSession(ctx context.Context, config *copilot.SessionConfig) (sdkSession, error) {
	session, err := c.client.CreateSession(ctx, config)
	if err != nil {
		return nil, err
	}
	return &sdkSessionAdapter{session: session}, nil
}

func (c *sdkClientAdapter) Stop() error { return c.client.Stop() }

type sdkSessionAdapter struct{ session *copilot.Session }

func (s *sdkSessionAdapter) SendAndWait(ctx context.Context, options copilot.MessageOptions) (*copilot.SessionEvent, error) {
	return s.session.SendAndWait(ctx, options)
}

func (s *sdkSessionAdapter) Disconnect() error { return s.session.Disconnect() }

func buildSDKClientOptions(options Options) (*copilot.ClientOptions, error) {
	copilotHome := strings.TrimSpace(options.CopilotHome)
	cliURL := strings.TrimSpace(options.CLIURL)

	sdkOptions := &copilot.ClientOptions{
		CLIUrl:    cliURL,
		AutoStart: copilot.Bool(options.AutoStart),
		LogLevel:  "error",
	}
	if copilotHome != "" {
		sdkOptions.Env = upsertEnvVar(os.Environ(), "COPILOT_HOME", copilotHome)
	}
	if cliURL != "" {
		if options.UseLoggedInUserSet && options.UseLoggedInUser {
			return nil, fmt.Errorf("configure copilot-sdk provider: UseLoggedInUser cannot be enabled when CLIURL is set; authenticate the external Copilot CLI server instead")
		}
		return sdkOptions, nil
	}
	useLoggedInUser := true
	if options.UseLoggedInUserSet {
		useLoggedInUser = options.UseLoggedInUser
	}
	sdkOptions.UseLoggedInUser = copilot.Bool(useLoggedInUser)
	return sdkOptions, nil
}

func buildSessionConfig(model string, sessionConfig analyzer.SessionConfig) *copilot.SessionConfig {
	wd := strings.TrimSpace(sessionConfig.WorkingDirectory)
	systemMessage := sessionConfig.SystemMessage
	if strings.TrimSpace(systemMessage) == "" {
		systemMessage = analyzer.BuildReadOnlySystemMessage(wd, sessionConfig.RunID)
	}
	return &copilot.SessionConfig{
		Model:               strings.TrimSpace(model),
		WorkingDirectory:    wd,
		OnPermissionRequest: buildPermissionHandler(wd),
		SystemMessage: &copilot.SystemMessageConfig{
			Mode:    "append",
			Content: systemMessage,
		},
	}
}

func buildPermissionHandler(workingDirectory string) copilot.PermissionHandlerFunc {
	normalizedRoot, rootErr := fsutil.NormalizeRootPath(workingDirectory)
	return func(request copilot.PermissionRequest, _ copilot.PermissionInvocation) (copilot.PermissionRequestResult, error) {
		if rootErr != nil {
			return permissionRejected(fmt.Sprintf("invalid project root %q: %v", workingDirectory, rootErr)), nil
		}
		decision := analyzer.DecidePermission(translateCopilotRequest(request), normalizedRoot)
		if decision.Approved {
			return permissionApproved(), nil
		}
		return permissionRejected(decision.Reason), nil
	}
}

func translateCopilotRequest(request copilot.PermissionRequest) analyzer.PermissionRequest {
	out := analyzer.PermissionRequest{
		Path:                    request.Path,
		PossiblePaths:           request.PossiblePaths,
		ReadOnly:                request.ReadOnly,
		HasWriteFileRedirection: request.HasWriteFileRedirection,
		FullCommandText:         request.FullCommandText,
	}
	if len(request.Commands) > 0 {
		out.Commands = make([]analyzer.ShellCommand, 0, len(request.Commands))
		for _, command := range request.Commands {
			out.Commands = append(out.Commands, analyzer.ShellCommand{
				Identifier: command.Identifier,
				ReadOnly:   command.ReadOnly,
			})
		}
	}
	switch request.Kind {
	case copilot.PermissionRequestKindRead:
		out.Kind = analyzer.PermissionKindRead
	case copilot.PermissionRequestKindURL:
		out.Kind = analyzer.PermissionKindURL
	case copilot.PermissionRequestKindShell:
		out.Kind = analyzer.PermissionKindShell
	case copilot.PermissionRequestKindMcp:
		out.Kind = analyzer.PermissionKindMCPTool
	case copilot.PermissionRequestKindCustomTool:
		out.Kind = analyzer.PermissionKindCustomTool
	default:
		out.Kind = PermissionKind(string(request.Kind))
	}
	return out
}

// PermissionKind alias used only to widen unknown kinds to the analyzer's type.
type PermissionKind = analyzer.PermissionKind

func permissionApproved() copilot.PermissionRequestResult {
	return copilot.PermissionRequestResult{Kind: copilot.PermissionRequestResultKindApproved}
}

func permissionRejected(reason string) copilot.PermissionRequestResult {
	denial := copilot.PermissionRequestResult{Kind: copilot.PermissionRequestResultKindRejected}
	if trimmed := strings.TrimSpace(reason); trimmed != "" {
		denial.Rules = []any{
			map[string]any{"decision": "deny", "reason": trimmed},
		}
	}
	return denial
}

func upsertEnvVar(env []string, key string, value string) []string {
	prefix := key + "="
	replaced := false
	merged := make([]string, 0, len(env)+1)
	for _, entry := range env {
		if strings.HasPrefix(entry, prefix) {
			if !replaced {
				merged = append(merged, prefix+value)
				replaced = true
			}
			continue
		}
		merged = append(merged, entry)
	}
	if !replaced {
		merged = append(merged, prefix+value)
	}
	return merged
}
