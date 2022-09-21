package remoteconfig

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	rc "github.com/DataDog/datadog-agent/pkg/remoteconfig/state"
)

type Callback func(*rc.Update)

// ClientConfig contains the required values to configure a remoteconfig client
type ClientConfig struct {
	// The address at which the agent is listening for remoteconfig update requests on
	AgentAddr string
	// The smenatic version of the user's application
	AppVersion string
	// The env this tracer is running in
	Env string
	// The rate at which the client should poll the agent for updates
	PollRate time.Duration
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
}

// A Client interacts with an Agent to update and track the state of remote
// configuration
type Client struct {
	ClientConfig
	client http.Client

	clientID   string
	repository *rc.Repository
	stop       chan struct{}

	callbacks map[string][]Callback

	lastError error
}

// NewClient creates a new remoteconfig Client
func NewClient(config ClientConfig) (*Client, error) {
	repo, err := rc.NewRepository([]byte(config.TUFRoot))
	if err != nil {
		return nil, err
	}

	// Tracers should always listen for FEATURES and it's not a traditional "product"
	// that would be enabled by a subsystem.
	config.Products = append(config.Products, rc.ProductFeatures)

	return &Client{
		ClientConfig: config,
		clientID:     generateID(),
		repository:   repo,
		stop:         make(chan struct{}),
		lastError:    nil,
		callbacks:    map[string][]Callback{},
	}, nil
}

// Start starts the client's update poll loop
func (c *Client) Start() {
	ticker := time.NewTicker(c.PollRate)
	defer ticker.Stop()

	for {
		select {
		case <-c.stop:
			return
		case <-ticker.C:
			c.updateState()
		}
	}
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

	url := fmt.Sprintf("http://%s/v0.7/config", c.AgentAddr)
	req, err := http.NewRequest("GET", url, &data)
	if err != nil {
		c.lastError = err
		return
	}

	resp, err := c.client.Do(req)
	if err != nil {
		c.lastError = err
		return
	}

	var update ClientGetConfigsResponse
	err = json.NewDecoder(resp.Body).Decode(&update)
	if err != nil {
		c.lastError = err
		return
	}

	err = c.applyUpdate(&update)
	c.lastError = err
}

func (c *Client) RegisterCallback(f Callback, product string) {
	c.callbacks[product] = append(c.callbacks[product], f)
}

func (c *Client) applyUpdate(pbUpdate *ClientGetConfigsResponse) error {
	fileMap := make(map[string][]byte, len(pbUpdate.TargetFiles))
	for _, f := range pbUpdate.TargetFiles {
		fileMap[f.Path] = f.Raw
	}

	update := rc.Update{
		TUFRoots:      pbUpdate.Roots,
		TUFTargets:    pbUpdate.Targets,
		TargetFiles:   fileMap,
		ClientConfigs: pbUpdate.ClientConfigs,
	}

	// XXX: At the moment we aren't hooking this into any tracer services, however
	// once we do we will need to update them based on the results from Update
	// (which is an array of product names that were updated during the request)
	//
	// The agent client does this by allowing services to register callback functions that receive
	// map[string]<CONFIG_TYPE>. The client provides a "register listener" function for each supported
	// product which stores the callbacks in a list. It's up to the service to use goroutines as needed
	// if the update application process would block the RC client for too long.
	products, err := c.repository.Update(update)
	// Performs the callbacks registered for all updated products
	for _, p := range products {
		for _, c := range c.callbacks[p] {
			c(&update)
		}
	}

	return err
}

func (c *Client) newUpdateRequest() (bytes.Buffer, error) {
	state, err := c.repository.CurrentState()
	if err != nil {
		return bytes.Buffer{}, err
	}

	pbCachedFiles := make([]*TargetFileMeta, 0, len(state.CachedFiles))
	for _, f := range state.CachedFiles {
		pbHashes := make([]*TargetFileHash, 0, len(f.Hashes))
		for alg, hash := range f.Hashes {
			pbHashes = append(pbHashes, &TargetFileHash{
				Algorithm: alg,
				Hash:      hex.EncodeToString(hash),
			})
		}
		pbCachedFiles = append(pbCachedFiles, &TargetFileMeta{
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

	pbConfigState := make([]*ConfigState, 0, len(state.Configs))
	for _, f := range state.Configs {
		pbConfigState = append(pbConfigState, &ConfigState{
			Id:      f.ID,
			Version: f.Version,
			Product: f.Product,
		})
	}

	req := ClientGetConfigsRequest{
		Client: &ClientData{
			State: &ClientState{
				RootVersion:    uint64(state.RootsVersion),
				TargetsVersion: uint64(state.TargetsVersion),
				ConfigStates:   pbConfigState,
				HasError:       hasError,
				Error:          errMsg,
			},
			Id:       c.clientID,
			Products: c.Products,
			IsTracer: true,
			ClientTracer: &ClientTracer{
				RuntimeId:     c.RuntimeID,
				Language:      "go",
				TracerVersion: c.TracerVersion,
				Service:       c.ServiceName,
				Env:           c.Env,
				AppVersion:    c.AppVersion,
			},
			Capabilities: []byte{64},
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
