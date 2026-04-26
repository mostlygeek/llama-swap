// Package adapters registers all protocol adapter implementations with the
// translate default registry. Import this package for side-effects from the
// proxy package to make translation functional.
package adapters

import (
	xl "github.com/mostlygeek/llama-swap/proxy/translate"
	"github.com/mostlygeek/llama-swap/proxy/translate/anthropic"
	"github.com/mostlygeek/llama-swap/proxy/translate/ollama"
	"github.com/mostlygeek/llama-swap/proxy/translate/openai"
)

func init() {
	xl.Register(xl.ProtocolOpenAI, openai.Parser{}, openai.Emitter{})
	xl.Register(xl.ProtocolAnthropic, anthropic.Parser{}, anthropic.Emitter{})
	xl.Register(xl.ProtocolOllama, ollama.Parser{}, ollama.Emitter{})

	xl.RegisterStream(xl.ProtocolOpenAI, openai.NewStreamParser, openai.NewStreamEmitter)
	xl.RegisterStream(xl.ProtocolAnthropic, anthropic.NewStreamParser, anthropic.NewStreamEmitter)
	xl.RegisterStream(xl.ProtocolOllama, ollama.NewStreamParser, ollama.NewStreamEmitter)
}
