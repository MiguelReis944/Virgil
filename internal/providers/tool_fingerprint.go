package providers

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"hash"
	"sort"
)

type toolStreamFingerprint struct {
	name hash.Hash
	args hash.Hash
}

type toolStreamKey struct {
	choice int
	tool   int
}

func newToolStreamFingerprint() *toolStreamFingerprint {
	return &toolStreamFingerprint{name: sha256.New(), args: sha256.New()}
}

func (f *toolStreamFingerprint) add(name, args string) {
	_, _ = f.name.Write([]byte(name))
	_, _ = f.args.Write([]byte(args))
}

func (f *toolStreamFingerprint) sum() string {
	combined := append(f.name.Sum(nil), f.args.Sum(nil)...)
	digest := sha256.Sum256(combined)
	return hex.EncodeToString(digest[:])
}

func (r *Result) addToolDelta(choiceIndex, toolIndex int, name, args string) error {
	if choiceIndex < 0 || choiceIndex >= 256 || toolIndex < 0 || toolIndex >= 256 {
		return errors.New("tool call index exceeds limit")
	}
	if r.toolStreams == nil {
		r.toolStreams = make(map[toolStreamKey]*toolStreamFingerprint)
	}
	key := toolStreamKey{choice: choiceIndex, tool: toolIndex}
	fingerprint := r.toolStreams[key]
	if fingerprint == nil {
		if len(r.toolStreams) >= 256 {
			return errors.New("too many tool calls")
		}
		fingerprint = newToolStreamFingerprint()
		r.toolStreams[key] = fingerprint
	}
	fingerprint.add(name, args)
	return nil
}

func (r *Result) finishToolDeltas() {
	indices := make([]toolStreamKey, 0, len(r.toolStreams))
	for index := range r.toolStreams {
		indices = append(indices, index)
	}
	sort.Slice(indices, func(i, j int) bool {
		if indices[i].choice == indices[j].choice {
			return indices[i].tool < indices[j].tool
		}
		return indices[i].choice < indices[j].choice
	})
	for _, index := range indices {
		r.ToolCallFingerprints = append(r.ToolCallFingerprints, r.toolStreams[index].sum())
	}
	r.toolStreams = nil
}
