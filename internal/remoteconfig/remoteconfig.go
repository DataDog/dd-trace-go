// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2022 Datadog, Inc.

package remoteconfig

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"os"
	"strings"
	"time"

	rc "github.com/DataDog/datadog-agent/pkg/remoteconfig/state"

	"gopkg.in/DataDog/dd-trace-go.v1/internal/globalconfig"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/version"
)

// Callback represents a function that can process a remote config update.
// A Callback function can be registered to a remote config client to automatically
// react upon receiving updates. This function returns the configuration processing status
// for each config file received through the update.
type Callback func(u ProductUpdate) map[string]rc.ApplyStatus

// Capability represents a bit index to be set in clientData.Capabilites in order to register a client
// for a specific capability
type Capability uint

const (
	_ Capability = iota
	// ASMActivation represents the capability to activate ASM through remote configuration
	ASMActivation
	// ASMIPBlocking represents the capability for ASM to block requests based on user IP
	ASMIPBlocking
	// ASMDDRules represents the capability to update the rules used by the ASM WAF for threat detection
	ASMDDRules
)

// DefaultClientConfig returns the default remote config client configuration
func DefaultClientConfig() ClientConfig {
	return ClientConfig{
		Env:           os.Getenv("DD_ENV"),
		HTTP:          &http.Client{Timeout: 10 * time.Second},
		PollInterval:  time.Second * 1,
		RuntimeID:     globalconfig.RuntimeID(),
		ServiceName:   globalconfig.ServiceName(),
		TracerVersion: version.Tag,
		TUFRoot:       os.Getenv("DD_RC_TUF_ROOT"),
	}
}

// ProductUpdate represents an update for a specific product.
// It is a map of file path to raw file content
type ProductUpdate map[string][]byte

// ClientConfig contains the required values to configure a remoteconfig client
type ClientConfig struct {
	// The address at which the agent is listening for remoteconfig update requests on
	AgentAddr string
	// The semantic version of the user's application
	AppVersion string
	// The env this tracer is running in
	Env string
	// The time interval between two client polls to the agent for updates
	PollInterval time.Duration
	// A list of remote config products this client is interested in
	Products []string
	// The tracer's runtime id
	RuntimeID string
	// The name of the user's application
	ServiceName string
	// The semantic version of the tracer
	TracerVersion string
	// The base TUF root metadata file
	TUFRoot string
	// The capabilities of the client
	Capabilities []Capability
	// HTTP is the HTTP client used to receive config updates
	HTTP *http.Client
}

// A Client interacts with an Agent to update and track the state of remote
// configuration
type Client struct {
	ClientConfig

	clientID   string
	endpoint   string
	repository *rc.Repository
	stop       chan struct{}

	callbacks map[string][]Callback

	lastError error
}

// NewClient creates a new remoteconfig Client
func NewClient(config ClientConfig) (*Client, error) {
	repo, err := rc.NewUnverifiedRepository()
	if err != nil {
		return nil, err
	}
	if config.HTTP == nil {
		config.HTTP = DefaultClientConfig().HTTP
	}

	return &Client{
		ClientConfig: config,
		clientID:     generateID(),
		endpoint:     fmt.Sprintf("http://%s/v0.7/config", config.AgentAddr),
		repository:   repo,
		stop:         make(chan struct{}),
		lastError:    nil,
		callbacks:    map[string][]Callback{},
	}, nil
}

// Start starts the client's update poll loop in a fresh goroutine
func (c *Client) Start() {
	go func() {
		ticker := time.NewTicker(c.PollInterval)
		defer ticker.Stop()

		for {
			select {
			case <-c.stop:
				return
			case <-ticker.C:
				c.updateState()
			}
		}
	}()
}

// Stop stops the client's update poll loop
func (c *Client) Stop() {
	close(c.stop)
}

func (c *Client) updateState() {
	data, err := c.newUpdateRequest()
	if err != nil {
		c.lastError = err
		return
	}

	req, err := http.NewRequest(http.MethodGet, c.endpoint, &data)
	if err != nil {
		c.lastError = err
		return
	}

	resp, err := c.HTTP.Do(req)
	if err != nil {
		c.lastError = err
		return
	}
	// Flush and close the response body when returning (cf. https://pkg.go.dev/net/http#Client.Do)
	defer func() {
		io.ReadAll(resp.Body)
		resp.Body.Close()
	}()

	var update clientGetConfigsResponse
	err = json.NewDecoder(resp.Body).Decode(&update)
	if err != nil {
		c.lastError = err
		return
	}

	err = c.applyUpdate(&update)
	c.lastError = err
}

// RegisterCallback allows registering a callback that will be invoked when the client
// receives a configuration update for the specified product.
func (c *Client) RegisterCallback(f Callback, product string) {
	c.callbacks[product] = append(c.callbacks[product], f)
}

func (c *Client) applyUpdate(pbUpdate *clientGetConfigsResponse) error {
	fileMap := make(map[string][]byte, len(pbUpdate.TargetFiles))
	productUpdates := make(map[string]ProductUpdate, len(c.Products))
	for _, f := range pbUpdate.TargetFiles {
		fileMap[f.Path] = f.Raw
		for _, p := range c.Products {
			productUpdates[p] = make(ProductUpdate)
			if strings.Contains(f.Path, p) {
				productUpdates[p][f.Path] = f.Raw
			}
		}
	}

	update := rc.Update{
		TUFRoots:      pbUpdate.Roots,
		TUFTargets:    pbUpdate.Targets,
		TargetFiles:   fileMap,
		ClientConfigs: pbUpdate.ClientConfigs,
	}

	mapify := func(s *rc.RepositoryState) map[string]string {
		m := make(map[string]string)
		for i := range s.Configs {
			path := s.CachedFiles[i].Path
			product := s.Configs[i].Product
			m[path] = product
		}
		return m
	}

	// Check the repository state before and after the update to detect which configs are not being sent anymore.
	// This is needed because some products can stop sending configurations, and we want to make sure that the subscribers
	// are provided with this information in this case
	stateBefore, _ := c.repository.CurrentState()
	products, err := c.repository.Update(update)
	stateAfter, _ := c.repository.CurrentState()

	// Create a config files diff between before/after the update to see which config files are missing
	mBefore := mapify(&stateBefore)
	mAfter := mapify(&stateAfter)
	for k := range mAfter {
		delete(mBefore, k)
	}

	// Set the payload data to nil for missing config files. The callbacks then can handle the nil config case to detect
	// that this config will not be updated anymore.
	updatedProducts := make(map[string]bool)
	for path, product := range mBefore {
		if productUpdates[product] == nil {
			productUpdates[product] = make(ProductUpdate)
		}
		productUpdates[product][path] = nil
		updatedProducts[product] = true
	}
	// Aggregate updated products and missing products so that callbacks get called for both
	for _, p := range products {
		updatedProducts[p] = true
	}

	// Performs the callbacks registered for all updated products and update the application status in the repository
	// (RCTE2)
	for p := range updatedProducts {
		for _, fn := range c.callbacks[p] {
			for path, status := range fn(productUpdates[p]) {
				c.repository.UpdateApplyStatus(path, status)
			}
		}
	}

	return err
}

func (c *Client) newUpdateRequest() (bytes.Buffer, error) {
	state, err := c.repository.CurrentState()
	if err != nil {
		return bytes.Buffer{}, err
	}
	// Temporary check while using untrusted repo, for which no initial root file is provided
	if state.RootsVersion < 1 {
		state.RootsVersion = 1
	}

	pbCachedFiles := make([]*targetFileMeta, 0, len(state.CachedFiles))
	for _, f := range state.CachedFiles {
		pbHashes := make([]*targetFileHash, 0, len(f.Hashes))
		for alg, hash := range f.Hashes {
			pbHashes = append(pbHashes, &targetFileHash{
				Algorithm: alg,
				Hash:      hex.EncodeToString(hash),
			})
		}
		pbCachedFiles = append(pbCachedFiles, &targetFileMeta{
			Path:   f.Path,
			Length: int64(f.Length),
			Hashes: pbHashes,
		})
	}

	hasError := c.lastError != nil
	errMsg := ""
	if hasError {
		errMsg = c.lastError.Error()
	}

	pbConfigState := make([]*configState, 0, len(state.Configs))
	for _, f := range state.Configs {
		pbConfigState = append(pbConfigState, &configState{
			ID:         f.ID,
			Version:    f.Version,
			Product:    f.Product,
			ApplyState: f.ApplyStatus.State,
			ApplyError: f.ApplyStatus.Error,
		})
	}

	cap := big.NewInt(0)
	for _, i := range c.Capabilities {
		cap.SetBit(cap, int(i), 1)
	}
	req := clientGetConfigsRequest{
		Client: &clientData{
			State: &clientState{
				RootVersion:    uint64(state.RootsVersion),
				TargetsVersion: uint64(state.TargetsVersion),
				ConfigStates:   pbConfigState,
				HasError:       hasError,
				Error:          errMsg,
			},
			ID:       c.clientID,
			Products: c.Products,
			IsTracer: true,
			ClientTracer: &clientTracer{
				RuntimeID:     c.RuntimeID,
				Language:      "go",
				TracerVersion: c.TracerVersion,
				Service:       c.ServiceName,
				Env:           c.Env,
				AppVersion:    c.AppVersion,
			},
			Capabilities: cap.Bytes(),
		},
		CachedTargetFiles: pbCachedFiles,
	}

	var b bytes.Buffer

	err = json.NewEncoder(&b).Encode(&req)
	if err != nil {
		return bytes.Buffer{}, err
	}

	return b, nil
}

var (
	idSize     = 21
	idAlphabet = []rune("_-0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ")
)

func generateID() string {
	bytes := make([]byte, idSize)
	_, err := rand.Read(bytes)
	if err != nil {
		panic(err)
	}
	id := make([]rune, idSize)
	for i := 0; i < idSize; i++ {
		id[i] = idAlphabet[bytes[i]&63]
	}
	return string(id[:idSize])
}
