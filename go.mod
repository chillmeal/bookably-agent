module github.com/chillmeal/bookably-agent

go 1.22

require (
	github.com/alicebob/miniredis/v2 v2.33.0
	github.com/anthropics/anthropic-sdk-go v0.2.0-alpha.4
	github.com/gin-gonic/gin v1.9.1
	github.com/go-telegram-bot-api/telegram-bot-api/v5 v5.5.1
	github.com/joho/godotenv v1.5.1
	github.com/kelseyhightower/envconfig v1.4.0
	github.com/redis/go-redis/v9 v9.5.1
	github.com/sashabaranov/go-openai v1.24.0
	github.com/stretchr/testify v1.9.0
	go.uber.org/zap v1.27.0
)

require (
	github.com/alicebob/gopher-json v0.0.0-20200520072559-a9ecdc9d1d3a // indirect
	github.com/cespare/xxhash/v2 v2.2.0 // indirect
	github.com/dgryski/go-rendezvous v0.0.0-20200823014737-9f7001d12a5f // indirect
	github.com/tidwall/gjson v1.14.4 // indirect
	github.com/tidwall/match v1.1.1 // indirect
	github.com/tidwall/pretty v1.2.1 // indirect
	github.com/tidwall/sjson v1.2.5 // indirect
	github.com/yuin/gopher-lua v1.1.1 // indirect
)

// Doc 06 lists "github.com/stretchr/mock", but that repository does not exist.
// Use testify/mock from github.com/stretchr/testify instead.
