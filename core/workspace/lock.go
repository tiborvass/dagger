package workspace

import (
	"fmt"
	"path/filepath"
	"sync"

	"github.com/dagger/dagger/util/lockfile"
)

const (
	LockDirName = ".dagger"

	LockFileName       = "dagger.lock"
	LegacyLockFileName = "lock"
	LegacyLockFilePath = LockDirName + "/" + LegacyLockFileName
)

// CanonicalLockFilePath maps the legacy .dagger/lock path to its dagger.lock
// sibling. Other paths are already canonical.
func CanonicalLockFilePath(lockFile string) string {
	if lockFile == "" {
		return ""
	}
	lockFile = filepath.Clean(lockFile)
	if filepath.Base(lockFile) != LegacyLockFileName {
		return lockFile
	}
	lockDir := filepath.Dir(lockFile)
	if filepath.Base(lockDir) != LockDirName {
		return lockFile
	}
	canonicalDir := filepath.Dir(lockDir)
	if canonicalDir == "." {
		return LockFileName
	}
	return filepath.Join(canonicalDir, LockFileName)
}

// LegacyLockFilePathForCanonical returns the legacy lockfile path that used to
// sit next to a canonical dagger.lock.
func LegacyLockFilePathForCanonical(lockFile string) string {
	lockDir := filepath.Dir(CanonicalLockFilePath(lockFile))
	return filepath.Join(lockDir, LegacyLockFilePath)
}

// LockPolicy controls update intent for a lock entry.
type LockPolicy string

const (
	PolicyPin LockPolicy = "pin"
)

// LookupResult is the stored lock result for a lookup tuple.
type LookupResult struct {
	Value  any        `json:"value"`
	Policy LockPolicy `json:"policy"`
}

// LookupEntry is a structured lockfile lookup tuple.
type LookupEntry struct {
	Namespace string
	Operation string
	Inputs    []any
	Result    LookupResult
}

// LookupOption is an optional input to a lock operation. Options are encoded
// as ordered key-value pairs after the entry value.
type LookupOption struct {
	Name  string
	Value any
}

// LookupInputs combines required positional inputs with optional named inputs.
func LookupInputs(required []any, options ...LookupOption) []any {
	inputs := append([]any(nil), required...)
	if len(options) == 0 {
		return inputs
	}
	pairs := make([]any, 0, len(options))
	for _, option := range options {
		pairs = append(pairs, []any{option.Name, option.Value})
	}
	return append(inputs, pairs)
}

// ParseLookupInputs separates required positional inputs from optional named
// inputs.
func ParseLookupInputs(inputs []any) ([]any, map[string]any, error) {
	required := inputs
	options := map[string]any{}
	if len(inputs) == 0 {
		return required, options, nil
	}
	pairs, ok := inputs[len(inputs)-1].([]any)
	if !ok || len(pairs) == 0 {
		return required, options, nil
	}
	for _, rawPair := range pairs {
		pair, ok := rawPair.([]any)
		if !ok || len(pair) != 2 {
			return inputs, nil, nil
		}
		name, ok := pair[0].(string)
		if !ok || name == "" {
			return inputs, nil, nil
		}
		if _, exists := options[name]; exists {
			return nil, nil, fmt.Errorf("duplicate lock option %q", name)
		}
		options[name] = pair[1]
	}
	return inputs[:len(inputs)-1], options, nil
}

// Lock is the workspace lockfile wrapper.
type Lock struct {
	mu   sync.RWMutex
	file *lockfile.Lockfile
}

// ParseLock parses dagger.lock data.
func ParseLock(data []byte) (*Lock, error) {
	file, err := lockfile.Parse(data)
	if err != nil {
		return nil, err
	}
	return &Lock{file: file}, nil
}

// NewLock returns an empty workspace lock.
func NewLock() *Lock {
	return &Lock{file: lockfile.New()}
}

// Marshal serializes lock entries.
func (l *Lock) Marshal() ([]byte, error) {
	if l == nil {
		return nil, fmt.Errorf("nil lock")
	}
	l.mu.RLock()
	defer l.mu.RUnlock()
	if l.file == nil {
		return nil, fmt.Errorf("nil lock")
	}
	return l.file.Marshal()
}

// Clone returns a deep copy of the lock.
func (l *Lock) Clone() (*Lock, error) {
	cloned := NewLock()
	if l == nil {
		return cloned, nil
	}
	if err := cloned.Merge(l); err != nil {
		return nil, err
	}
	return cloned, nil
}

// Merge applies all entries from other onto l.
func (l *Lock) Merge(other *Lock) error {
	if l == nil {
		return fmt.Errorf("nil lock")
	}
	l.mu.RLock()
	initialized := l.file != nil
	l.mu.RUnlock()
	if !initialized {
		return fmt.Errorf("nil lock")
	}
	if other == nil {
		return nil
	}
	entries, err := other.Entries()
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if err := l.setLookup(entry.Namespace, entry.Operation, entry.Inputs, entry.Result); err != nil {
			return err
		}
	}
	return nil
}

// GetLookup retrieves the lock result for a generic lookup tuple.
func (l *Lock) GetLookup(namespace, operation string, inputs []any) (LookupResult, bool, error) {
	if l == nil {
		return LookupResult{}, false, nil
	}
	l.mu.RLock()
	defer l.mu.RUnlock()
	if l.file == nil {
		return LookupResult{}, false, nil
	}
	value, ok := l.file.Get(namespace, operation, inputs)
	if !ok {
		return LookupResult{}, false, nil
	}
	result, err := parseLookupResult(value)
	if err != nil {
		return LookupResult{}, false, err
	}
	return result, true, nil
}

// SetLookup sets the lock result for a generic lookup tuple.
func (l *Lock) SetLookup(namespace, operation string, inputs []any, result LookupResult) error {
	return l.setLookup(namespace, operation, inputs, result)
}

func (l *Lock) setLookup(namespace, operation string, inputs []any, result LookupResult) error {
	if l == nil {
		return fmt.Errorf("nil lock")
	}
	if result.Value == nil {
		return fmt.Errorf("lookup value is required")
	}
	if value, ok := result.Value.(string); ok && value == "" {
		return fmt.Errorf("lookup value is required")
	}
	if !isValidLockPolicy(result.Policy) {
		return fmt.Errorf("invalid lock policy %q", result.Policy)
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file == nil {
		return fmt.Errorf("nil lock")
	}
	return l.file.Set(namespace, operation, inputs, result.Value)
}

// DeleteLookup removes a generic lookup tuple entry.
func (l *Lock) DeleteLookup(namespace, operation string, inputs []any) bool {
	if l == nil {
		return false
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file == nil {
		return false
	}
	return l.file.Delete(namespace, operation, inputs)
}

// Entries returns a deterministic snapshot of all lookup entries.
func (l *Lock) Entries() ([]LookupEntry, error) {
	if l == nil {
		return nil, nil
	}
	l.mu.RLock()
	defer l.mu.RUnlock()
	if l.file == nil {
		return nil, nil
	}

	rawEntries := l.file.Entries()
	entries := make([]LookupEntry, 0, len(rawEntries))
	for _, entry := range rawEntries {
		result, err := parseLookupResult(entry.Value)
		if err != nil {
			return nil, err
		}
		entries = append(entries, LookupEntry{
			Namespace: entry.Namespace,
			Operation: entry.Operation,
			Inputs:    entry.Inputs,
			Result:    result,
		})
	}
	return entries, nil
}

func parseLookupResult(value any) (LookupResult, error) {
	if value == nil {
		return LookupResult{}, fmt.Errorf("value is required")
	}
	if resultValue, ok := value.(string); ok && resultValue == "" {
		return LookupResult{}, fmt.Errorf("value is required")
	}
	result := LookupResult{
		Value:  value,
		Policy: PolicyPin,
	}
	if !isValidLockPolicy(result.Policy) {
		return LookupResult{}, fmt.Errorf("invalid policy %q", result.Policy)
	}
	return result, nil
}

func isValidLockPolicy(policy LockPolicy) bool {
	return policy == PolicyPin
}
