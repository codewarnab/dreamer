package analyzer

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	copilot "github.com/github/copilot-sdk/go"
)

type ClientOptions struct {
	CopilotHome      string
	UseLoggedInUser  bool
	CLIURL           string
	AutoStart        bool
	Model            string
	WorkingDirectory string
}

type Session interface {
	Run(ctx context.Context, prompt string, timeout time.Duration) (string, error)
	Close() error
}

type Client interface {
	Start(ctx context.Context) error
	NewSession(ctx context.Context) (Session, error)
	Close() error
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

func NewClient(options ClientOptions) (Client, error) {
	sdkOptions, err := buildSDKClientOptions(options)
	if err != nil {
		return nil, err
	}

	client := &copilotClient{
		options: options,
		client:  newSDKClient(sdkOptions),
	}

	if options.AutoStart {
		if err := client.Start(context.Background()); err != nil {
			return nil, err
		}
	}
	return client, nil
}

type copilotClient struct {
	options ClientOptions
	client  sdkClient
}

func (c *copilotClient) Start(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := c.client.Start(ctx); err != nil {
		return fmt.Errorf("start analyzer client: unable to start Copilot SDK client; ensure Copilot CLI is installed and authenticated (run `copilot auth login`): %w", err)
	}
	return nil
}

func (c *copilotClient) NewSession(ctx context.Context) (Session, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := c.Start(ctx); err != nil {
		return nil, err
	}

	requestedModel := strings.TrimSpace(c.options.Model)
	sessionConfig := buildSessionConfig(requestedModel, c.options.WorkingDirectory)

	session, err := c.client.CreateSession(ctx, sessionConfig)
	if err != nil {
		if requestedModel == "" {
			return nil, fmt.Errorf("create analyzer session: unable to create Copilot session; check auth and model availability: %w", err)
		}

		// Fallback to SDK auto-model selection when an explicit model fails.
		fallbackSession, fallbackErr := c.client.CreateSession(ctx, buildSessionConfig("", c.options.WorkingDirectory))
		if fallbackErr != nil {
			return nil, fmt.Errorf("create analyzer session: unable to create Copilot session with requested model %q (%v) and SDK auto-model fallback (%w)", requestedModel, err, fallbackErr)
		}
		return &copilotSession{session: fallbackSession}, nil
	}

	return &copilotSession{session: session}, nil
}

func (c *copilotClient) Close() error {
	if err := c.client.Stop(); err != nil {
		return fmt.Errorf("close analyzer client: unable to stop Copilot SDK client cleanly: %w", err)
	}
	return nil
}

type copilotSession struct {
	session sdkSession
}

func (s *copilotSession) Run(ctx context.Context, prompt string, timeout time.Duration) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}

	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	event, err := s.session.SendAndWait(ctx, copilot.MessageOptions{
		Prompt: prompt,
	})
	if err != nil {
		return "", fmt.Errorf("run analyzer prompt: Copilot request failed; verify authentication and connectivity: %w", err)
	}
	if event == nil || event.Data == nil {
		return "", fmt.Errorf("run analyzer prompt: Copilot session completed without assistant output")
	}

	message, ok := event.Data.(*copilot.AssistantMessageData)
	if !ok {
		return "", fmt.Errorf("run analyzer prompt: unexpected response type %T", event.Data)
	}

	return message.Content, nil
}

func (s *copilotSession) Close() error {
	if err := s.session.Disconnect(); err != nil {
		return fmt.Errorf("close analyzer session: unable to disconnect Copilot session cleanly: %w", err)
	}
	return nil
}

type sdkClientAdapter struct {
	client *copilot.Client
}

func (c *sdkClientAdapter) Start(ctx context.Context) error {
	return c.client.Start(ctx)
}

func (c *sdkClientAdapter) CreateSession(ctx context.Context, config *copilot.SessionConfig) (sdkSession, error) {
	session, err := c.client.CreateSession(ctx, config)
	if err != nil {
		return nil, err
	}
	return &sdkSessionAdapter{session: session}, nil
}

func (c *sdkClientAdapter) Stop() error {
	return c.client.Stop()
}

type sdkSessionAdapter struct {
	session *copilot.Session
}

func (s *sdkSessionAdapter) SendAndWait(ctx context.Context, options copilot.MessageOptions) (*copilot.SessionEvent, error) {
	return s.session.SendAndWait(ctx, options)
}

func (s *sdkSessionAdapter) Disconnect() error {
	return s.session.Disconnect()
}

func buildSDKClientOptions(options ClientOptions) (*copilot.ClientOptions, error) {
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
		if options.UseLoggedInUser {
			return nil, fmt.Errorf("configure analyzer client: UseLoggedInUser cannot be enabled when CLIURL is set; authenticate the external Copilot CLI server instead")
		}
		return sdkOptions, nil
	}

	useLoggedInUser := options.UseLoggedInUser
	if !useLoggedInUser {
		useLoggedInUser = true
	}
	sdkOptions.UseLoggedInUser = copilot.Bool(useLoggedInUser)

	return sdkOptions, nil
}

func buildSessionConfig(model string, workingDirectory string) *copilot.SessionConfig {
	trimmedWorkingDirectory := strings.TrimSpace(workingDirectory)
	return &copilot.SessionConfig{
		Model:               strings.TrimSpace(model),
		WorkingDirectory:    trimmedWorkingDirectory,
		OnPermissionRequest: buildReadOnlyPermissionHandler(trimmedWorkingDirectory),
		SystemMessage: &copilot.SystemMessageConfig{
			Mode:    "append",
			Content: buildAnalysisSystemMessage(trimmedWorkingDirectory),
		},
	}
}

func buildAnalysisSystemMessage(workingDirectory string) string {
	if strings.TrimSpace(workingDirectory) == "" {
		return "You are in read-only repository analysis mode.\n" +
			"Analyze the provided chat/discussion context first.\n" +
			"If additional evidence is needed, use targeted read/search inspection only.\n" +
			"Allowed actions: read/search operations, read-only shell commands, read-only MCP/custom tools (including embeddings/search), and web URL fetch/search.\n" +
			"Forbidden actions: file create/edit/delete, git history rewriting, branch mutation, or any destructive command.\n" +
			"Do not scan files aimlessly; inspect only relevant files suggested by the chat context."
	}

	return "You are in read-only repository analysis mode.\n" +
		"Analyze the provided chat/discussion context first.\n" +
		"If additional evidence is needed, use targeted read/search inspection only.\n" +
		"Allowed actions: read/search operations, read-only shell commands, read-only MCP/custom tools (including embeddings/search), and web URL fetch/search.\n" +
		fmt.Sprintf("Scope boundary: inspect only files under the project directory %q unless the request is an explicit web fetch.\n", workingDirectory) +
		"Forbidden actions: file create/edit/delete, git history rewriting, branch mutation, or any destructive command.\n" +
		"Do not scan files aimlessly; inspect only relevant files suggested by the chat context."
}

func buildReadOnlyPermissionHandler(workingDirectory string) copilot.PermissionHandlerFunc {
	return func(request copilot.PermissionRequest, _ copilot.PermissionInvocation) (copilot.PermissionRequestResult, error) {
		switch request.Kind {
		case copilot.PermissionRequestKindRead:
			if requestPathAllowed(request.Path, workingDirectory) && requestPathsAllowed(request.PossiblePaths, workingDirectory) {
				return permissionApproved(), nil
			}
			return permissionRejected(), nil
		case copilot.PermissionRequestKindURL:
			return permissionApproved(), nil
		case copilot.PermissionRequestKindShell:
			if shellRequestReadOnly(request) && requestPathsAllowed(request.PossiblePaths, workingDirectory) {
				return permissionApproved(), nil
			}
			return permissionRejected(), nil
		case copilot.PermissionRequestKindMcp, copilot.PermissionRequestKindCustomTool:
			if request.ReadOnly != nil && *request.ReadOnly {
				return permissionApproved(), nil
			}
			return permissionRejected(), nil
		default:
			return permissionRejected(), nil
		}
	}
}

func shellRequestReadOnly(request copilot.PermissionRequest) bool {
	if request.HasWriteFileRedirection != nil && *request.HasWriteFileRedirection {
		return false
	}
	if request.ReadOnly != nil {
		return *request.ReadOnly
	}
	if len(request.Commands) == 0 {
		return false
	}
	for _, command := range request.Commands {
		if !command.ReadOnly {
			return false
		}
	}
	return true
}

func requestPathAllowed(requestPath *string, root string) bool {
	if requestPath == nil {
		return true
	}
	return pathWithinRoot(*requestPath, root)
}

func requestPathsAllowed(paths []string, root string) bool {
	for _, path := range paths {
		if !pathWithinRoot(path, root) {
			return false
		}
	}
	return true
}

func pathWithinRoot(path string, root string) bool {
	trimmedRoot := strings.TrimSpace(root)
	if trimmedRoot == "" {
		return true
	}

	trimmedPath := strings.TrimSpace(path)
	if trimmedPath == "" {
		return true
	}

	absoluteRoot, err := filepath.Abs(trimmedRoot)
	if err != nil {
		return false
	}
	absolutePath, err := filepath.Abs(trimmedPath)
	if err != nil {
		return false
	}

	cleanRoot := filepath.Clean(absoluteRoot)
	cleanPath := filepath.Clean(absolutePath)
	if strings.EqualFold(cleanPath, cleanRoot) {
		return true
	}

	rootWithSeparator := cleanRoot + string(filepath.Separator)
	return strings.HasPrefix(strings.ToLower(cleanPath), strings.ToLower(rootWithSeparator))
}

func permissionApproved() copilot.PermissionRequestResult {
	return copilot.PermissionRequestResult{Kind: copilot.PermissionRequestResultKindApproved}
}

func permissionRejected() copilot.PermissionRequestResult {
	return copilot.PermissionRequestResult{Kind: copilot.PermissionRequestResultKindRejected}
}

func upsertEnvVar(env []string, key string, value string) []string {
	prefix := key + "="
	replaced := false
	result := make([]string, 0, len(env)+1)

	for _, entry := range env {
		if strings.HasPrefix(entry, prefix) {
			if !replaced {
				result = append(result, prefix+value)
				replaced = true
			}
			continue
		}
		result = append(result, entry)
	}

	if !replaced {
		result = append(result, prefix+value)
	}

	return result
}
