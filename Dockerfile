# Baseline container definition for Bookably Agent.
# Application build steps will be expanded when cmd/agent is implemented.

FROM golang:1.22-alpine AS base

WORKDIR /app

# Placeholder default command for bootstrap stage.
CMD ["sh", "-c", "echo 'bookably-agent bootstrap image (no app binary yet)' && sleep infinity"]
