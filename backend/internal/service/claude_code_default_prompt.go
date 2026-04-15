package service

import _ "embed"

//go:embed prompts/claude_code_default_agent.txt
var defaultClaudeCodeAgentPrompt string

//go:embed prompts/claude_code_default_env.txt
var defaultClaudeCodeEnvPrompt string
