package glacier

import (
	"crypto/sha256"
	"encoding/hex"
	"hash"
)

// MultiTreeHasher is used to calculate tree hashes for multi-part uploads
// Call Add sequentially on hashes you have calculated them for
// parts individually, and CreateHash to get the resulting root-level
// hash to use in a CompleteMultipart request.
type MultiTreeHasher struct {
	nodes [][sha256.Size]byte
}

// Add appends the hex-encoded hash to the treehash as a new node
// Add must be called sequentially on parts.
func (t *MultiTreeHasher) Add(hash string) {
	var b [sha256.Size]byte
	hex.Decode(b[:], []byte(hash))
	t.nodes = append(t.nodes, b)
}

// CreateHash returns the root-level hex-encoded hash to send in the
// CompleteMultipart request.
func (t *MultiTreeHasher) CreateHash() string {
	if len(t.nodes) == 0 {
		return ""
	}
	rootHash := treeHash(t.nodes)
	return hex.EncodeToString(rootHash[:])
}

// treeHash calculates the root-level treeHash given sequential
// leaf nodes.
func treeHash(nodes [][sha256.Size]byte) [sha256.Size]byte {
	var combine [sha256.Size * 2]byte
	for len(nodes) > 1 {
		for i := 0; i < len(nodes)/2; i++ {
			copy(combine[:sha256.Size], nodes[i*2][:])
			copy(combine[sha256.Size:], nodes[i*2+1][:])
			nodes[i] = sha256.Sum256(combine[:])
		}
		if len(nodes)%2 == 0 {
			nodes = nodes[:len(nodes)/2]
		} else {
			nodes[len(nodes)/2] = nodes[len(nodes)-1]
			nodes = nodes[:len(nodes)/2+1]
		}
	}
	return nodes[0]
}

// TreeHash is used to calculate the tree hash and regular sha256 hash of the
// data written to it. These values are needed when uploading an archive or
// verifying an aligned download. First each 1 MiB chunk of data is hashed.
// Second each consecutive child node's hashes are concatenated then hashed (if
// there is a single node left it is promoted to the next level). The second
// step is repeated until there is only a single node, this is the tree hash.
// See docs.aws.amazon.com/amazonglacier/latest/dev/checksum-calculations.html
type TreeHash struct {
	remaining   []byte
	nodes       [][sha256.Size]byte
	runningHash hash.Hash         // linear
	treeHash    [sha256.Size]byte // computed
	linearHash  [sha256.Size]byte // computed
}

// NewTreeHash returns an new, initialized tree hasher.
func NewTreeHash() *TreeHash {
	result := &TreeHash{
		runningHash: sha256.New(),
		remaining:   make([]byte, 0, 1<<20),
	}
	result.Reset()
	return result
}

// Reset the tree hash's state allowing it to be reused.
func (th *TreeHash) Reset() {
	th.runningHash.Reset()
	th.remaining = th.remaining[:0]
	th.nodes = th.nodes[:0]
	th.treeHash = [sha256.Size]byte{}
	th.linearHash = [sha256.Size]byte{}
}

// Write writes all of p, storing every 1 MiB of data's hash.
func (th *TreeHash) Write(p []byte) (int, error) {
	n := len(p)

	// Not enough data to fill a 1 MB chunk.
	if len(th.remaining)+len(p) < 1<<20 {
		th.remaining = append(th.remaining, p...)
		return n, nil
	}

	// Move enough to fill th.remaining to 1 MB.
	fill := 1<<20 - len(th.remaining)
	th.remaining = append(th.remaining, p[:fill]...)
	p = p[fill:]

	// Append the 1 MB in th.remaining.
	th.nodes = append(th.nodes, sha256.Sum256(th.remaining))
	th.runningHash.Write(th.remaining)
	th.remaining = th.remaining[:0]

	// Append all 1M chunks remaining in p.
	for len(p) >= 1<<20 {
		th.nodes = append(th.nodes, sha256.Sum256(p[:1<<20]))
		th.runningHash.Write(p[:1<<20])
		p = p[1<<20:]
	}

	// Copy what remains in p to th.remaining.
	th.remaining = append(th.remaining, p...)

	return n, nil
}

// Close closes the the remaing chunks of data and then calculates the tree hash.
func (th *TreeHash) Close() error {
	// create last node; it is impossible that it has a size > 1 MB
	if len(th.remaining) > 0 {
		th.nodes = append(th.nodes, sha256.Sum256(th.remaining))
		th.runningHash.Write(th.remaining)
	}
	// Calculate the tree and linear hashes
	if len(th.nodes) > 0 {
		th.treeHash = treeHash(th.nodes)
	}
	th.runningHash.Sum(th.linearHash[:0])
	return nil
}

// TreeHash returns the root-level tree hash of everything written.
func (th *TreeHash) TreeHash() []byte {
	return th.treeHash[:]
}

// Hash returns the linear sha256 checksum of everything written.
func (th *TreeHash) Hash() []byte {
	return th.linearHash[:]
}
