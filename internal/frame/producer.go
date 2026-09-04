package frame

import (
	"crypto/sha256"
	"encoding/json"
	"sync"
	"time"

	"github.com/M1nt-Ch0c0/ai-quota-frame/internal/quota"
)

// Renderer turns one quota snapshot into the PNG sent to a display.
type Renderer interface {
	Render(quota.Snapshot) ([]byte, error)
}

// Image is one rendered frame. Payload must be treated as immutable.
type Image struct {
	Payload []byte
	Digest  [sha256.Size]byte
}

// Producer renders and caches frames by the fields that can affect the image.
// It serializes rendering so repeated device pushes share exactly one payload
// for the same semantic snapshot.
type Producer struct {
	mu       sync.Mutex
	renderer Renderer
	cache    cachedImage
}

type cachedImage struct {
	key   [sha256.Size]byte
	image Image
	valid bool
}

func NewProducer(renderer Renderer) *Producer {
	return &Producer{renderer: renderer}
}

func (producer *Producer) Produce(snapshot quota.Snapshot) (Image, error) {
	key, err := contentKey(snapshot)
	if err != nil {
		return Image{}, err
	}

	producer.mu.Lock()
	defer producer.mu.Unlock()
	if producer.cache.valid && producer.cache.key == key {
		return producer.cache.image, nil
	}

	payload, err := producer.renderer.Render(snapshot)
	if err != nil {
		return Image{}, err
	}
	digest := sha256.Sum256(payload)
	image := Image{
		Payload: payload,
		Digest:  digest,
	}
	producer.cache = cachedImage{key: key, image: image, valid: true}
	return image, nil
}

func contentKey(snapshot quota.Snapshot) ([sha256.Size]byte, error) {
	accounts := append([]quota.Account(nil), snapshot.Accounts...)
	for accountIndex := range accounts {
		accounts[accountIndex].Name = ""
		accounts[accountIndex].LastFreshAt = nil
		accounts[accountIndex].Windows = append([]quota.Window(nil), accounts[accountIndex].Windows...)
		for windowIndex := range accounts[accountIndex].Windows {
			window := &accounts[accountIndex].Windows[windowIndex]
			window.UsedPercent = nil
			window.ObservedAt = nil
			if window.Source != "demo" {
				window.Source = ""
			}
			if window.ResetsAt != nil {
				value := window.ResetsAt.UTC().Truncate(time.Minute)
				window.ResetsAt = &value
			}
		}
	}
	usage := snapshot.Usage
	if usage != nil {
		copyUsage := *usage
		if copyUsage.Source != "demo" {
			copyUsage.Source = ""
		}
		usage = &copyUsage
	}
	payload, err := json.Marshal(struct {
		DataUpdatedAt time.Time       `json:"data_updated_at"`
		Stale         bool            `json:"stale"`
		Accounts      []quota.Account `json:"accounts"`
		Usage         *quota.Usage    `json:"usage,omitempty"`
		Errors        []string        `json:"errors,omitempty"`
	}{
		DataUpdatedAt: snapshot.DataUpdatedAt,
		Stale:         snapshot.Stale,
		Accounts:      accounts,
		Usage:         usage,
		Errors:        snapshot.Errors,
	})
	if err != nil {
		return [sha256.Size]byte{}, err
	}
	return sha256.Sum256(payload), nil
}
