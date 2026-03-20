# Security Policy

## Reporting a Vulnerability

If you discover a security vulnerability in this project, please report it responsibly:

1. **Do not open a public GitHub issue** for security vulnerabilities
2. **Email security details to** the project maintainer privately
3. **Include**:
   - Description of the vulnerability
   - Steps to reproduce
   - Potential impact
   - Suggested fix (if available)

The maintainer will:
- Acknowledge receipt within 48 hours
- Provide an estimated timeline for a fix
- Keep you informed of progress
- Credit you in the advisory (unless you request anonymity)

## Supported Versions

| Version | Status | Supported Until |
|---------|--------|-----------------|
| 1.0.x   | Current | Next major release + 6 months |
| 0.x     | EOL    | Not supported |

## Security Patch Timeline

- **Critical (CVSS ≥ 9.0)**: Fixed within 7 days of report
- **High (CVSS 7.0-8.9)**: Fixed within 14 days of report
- **Medium (CVSS 4.0-6.9)**: Fixed in next minor release (typically within 30 days)
- **Low (CVSS < 4.0)**: Fixed in next regular release

## Known Security Considerations

### 1. Credential Storage

**Risk**: API keys are stored in `~/.gi/auth.json`

**Mitigations**:
- File permissions are set to 0644 (user read-write, others read)
- Consider using environment variables instead for sensitive deployments
- Support for shell command resolution (`!command` syntax) allows dynamic secret retrieval

**Best Practices**:
```bash
# Preferred: Use environment variables
export ANTHROPIC_API_KEY="sk-ant-..."

# Alternative: Shell command resolution in auth.json
{
  "anthropic": {
    "type": "api_key",
    "key": "!pass anthropic"  # Retrieves from password manager
  }
}
```

### 2. Shell Command Execution

**Risk**: BashTool executes arbitrary shell commands via `/bin/bash -c`

**Mitigations**:
- Timeouts enforce execution limits (default 120s, max 600s)
- Output truncated to 100KB
- Process group SIGKILL ensures termination

**Trust Boundary**:
- Commands execute in the user's context with their permissions
- Only use gi with trusted AI models or in isolated environments
- Consider using `--cwd` to sandbox execution to a specific directory

### 3. File Operations

**Risk**: WriteTool and Glob tools operate on the filesystem

**Mitigations**:
- GlobTool limits recursive depth (max 64), prevents excessive ** segments
- GlobTool limits results (max 1000)
- Both tools resolve paths to absolute paths

**Considerations**:
- WriteTool can write to any path accessible by the user
- Use `--cwd` to limit file operations to a specific project directory
- Avoid running gi with elevated privileges

### 4. Session Data

**Risk**: Sessions stored in `~/.gi/sessions/` may contain conversation history

**Mitigations**:
- Sessions are local to the user's home directory
- File permissions follow umask defaults
- Consider deleting sensitive sessions with `/clear`

**Best Practice**:
- Avoid discussing sensitive credentials in conversations
- Use environment variables for API keys instead of embedding in prompts

### 5. Model/Provider Trust

**Risk**: Using untrusted or compromised AI models

**Mitigations**:
- Always verify provider authenticity and API endpoints
- Use only official provider SDKs
- Monitor for unexpected API usage patterns

**Supported Providers**:
- Anthropic (claude.ai)
- OpenAI (api.openai.com)
- Azure OpenAI (authenticated via key)
- AWS Bedrock (via AWS credentials)
- Google Gemini (api.generativeai.google.com)
- OpenRouter (openrouter.ai)

## Dependencies Security

This project dependencies are regularly scanned for vulnerabilities:

```bash
# Verify dependencies
go list -m all
go mod tidy
go mod verify
```

Key dependencies:
- `charmbracelet/*`: Terminal UI libraries (maintained)
- `aws/aws-sdk-go-v2`: AWS client SDK (maintained)
- Standard library only for core crypto operations

## Security Features

### Authentication
- PKCE flow support for OAuth providers
- Secure token refresh with 5-minute buffer before expiration
- Credential isolation per provider

### Command Execution
- Process group isolation for timeout handling
- Context-based cancellation
- Output size limits to prevent memory exhaustion

### File Operations
- Pattern matching limits to prevent DoS
- Absolute path normalization
- Directory traversal prevention in glob operations

## Recommendations for Safe Usage

1. **Environment Setup**
   - Run gi in a dedicated directory or virtual environment
   - Use `--cwd` flag to sandbox execution
   - Never run with `sudo` or elevated privileges

2. **Credential Management**
   - Prefer environment variables over stored credentials
   - Rotate API keys regularly
   - Use short-lived tokens when possible

3. **Model Selection**
   - Use models you trust with your codebase
   - Consider using extended thinking for complex/risky operations
   - Review model outputs before applying to production code

4. **Session Handling**
   - Clear sensitive sessions after use
   - Don't share session IDs with untrusted parties
   - Audit session history before sharing code

## Security Limitations

This tool is designed for local use by trusted users in a single-user environment. It is **not** suitable for:
- Multi-user systems without isolation
- Untrusted input processing
- Production code without human review
- Handling highly sensitive data

## Contact

For security policy inquiries or responsible disclosure, contact the project maintainer.
