package jsonnetprovider

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/google/go-jsonnet"
	"go.opentelemetry.io/collector/confmap"
)

type provider struct {
	settings *confmap.ProviderSettings
}

// Retrieve implements confmap.Provider.
func (p *provider) Retrieve(ctx context.Context, uri string, watcher confmap.WatcherFunc) (*confmap.Retrieved, error) {
	schemeAndPath := strings.SplitN(uri, "://", 2)
	if len(schemeAndPath) != 2 {
		return nil, fmt.Errorf("invalid uri: %s", uri)
	}
	scheme := schemeAndPath[0]
	path := schemeAndPath[1]
	if scheme != "jsonnet" {
		return nil, fmt.Errorf("invalid scheme: %s", scheme)
	}

	vm := jsonnet.MakeVM()
	fmt.Println("path", path)
	file, err := os.OpenFile(path, os.O_RDONLY, 0)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	code, err := io.ReadAll(file)
	if err != nil {
		return nil, err
	}
	result, err := vm.EvaluateAnonymousSnippet(path, string(code))
	if err != nil {
		return nil, err
	}

	ret := map[string]any{}
	err = json.Unmarshal([]byte(result), &ret)
	if err != nil {
		return nil, err
	}
	return confmap.NewRetrieved(ret)
}

// Scheme implements confmap.Provider.
func (p *provider) Scheme() string {
	return "jsonnet"
}

// Shutdown implements confmap.Provider.
func (p *provider) Shutdown(ctx context.Context) error {
	return nil
}

func Create(ps confmap.ProviderSettings) confmap.Provider {
	return &provider{
		settings: &ps,
	}
}

func NewFactory() confmap.ProviderFactory {
	return confmap.NewProviderFactory(Create)
}
