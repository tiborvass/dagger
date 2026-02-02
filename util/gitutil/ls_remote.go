package gitutil

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/dagger/dagger/util/hashutil"
	"github.com/opencontainers/go-digest"
)

type Remote struct {
	Refs    []*Ref
	RefMap  map[string]*Ref
	Symrefs map[string]string

	// override what HEAD points to, if set
	Head *Ref
}

func NewRemote() *Remote {
	return &Remote{RefMap: map[string]*Ref{}, Symrefs: map[string]string{}}
}

type Ref struct {
	// Name is the fully resolved ref name, e.g. refs/heads/main or refs/tags/v1.0.0 or a commit SHA
	Name string

	// SHA is the commit SHA the ref points to
	SHA string
}

func (r *Ref) ShortName() string {
	if IsCommitSHA(r.Name) {
		return r.Name
	}
	if name, ok := strings.CutPrefix(r.Name, "refs/heads/"); ok {
		return name
	}
	if name, ok := strings.CutPrefix(r.Name, "refs/tags/"); ok {
		return name
	}
	if name, ok := strings.CutPrefix(r.Name, "refs/remotes/"); ok {
		return name
	}
	if name, ok := strings.CutPrefix(r.Name, "refs/"); ok {
		return name
	}
	return r.Name
}

func (r *Ref) Digest() digest.Digest {
	return hashutil.HashStrings(r.Name, r.SHA)
}

func (cli *GitCLI) LsRemote(ctx context.Context, remote string) (*Remote, error) {
	out, err := cli.Run(ctx,
		"ls-remote",
		"--symref",
		remote,
	)
	if err != nil {
		return nil, err
	}
	lines := strings.Split(string(out), "\n")

	refs := make([]*Ref, 0, len(lines))
	refMap := make(map[string]*Ref, len(lines))
	symrefs := make(map[string]string)

	for _, line := range lines {
		k, v, ok := strings.Cut(line, "\t")
		if !ok {
			continue
		}

		if target, ok := strings.CutPrefix(k, "ref: "); ok {
			// this is a symref, record it for later
			symrefs[v] = target
		} else {
			// normal ref
			ref := &Ref{SHA: k, Name: v}
			// assuming server returns the ref list in alphabetical order
			refs = append(refs, ref)
			refMap[v] = ref
		}
	}

	return &Remote{
		RefMap:  refMap,
		Refs:    refs,
		Symrefs: symrefs,
	}, nil
}

func (remote *Remote) Digest() digest.Digest {
	inputs := []string{}
	for _, ref := range remote.Refs {
		inputs = append(inputs, "ref", ref.Digest().String(), "\x00")
	}
	if remote.Head != nil {
		inputs = append(inputs, "head", remote.Head.Digest().String(), "\x00")
	}
	return hashutil.HashStrings(inputs...)
}

func (remote *Remote) withRefs(refs []*Ref) *Remote {
	return &Remote{
		Refs:    refs,
		Symrefs: remote.Symrefs,
		Head:    remote.Head,
	}
}

func (remote *Remote) Tags() *Remote {
	var tags []*Ref
	for _, ref := range remote.Refs {
		if !strings.HasPrefix(ref.Name, "refs/tags/") {
			continue // skip non-tags
		}
		if strings.HasSuffix(ref.Name, "^{}") {
			continue // skip unpeeled tags, we'll include the peeled version instead
		}
		tags = append(tags, ref)
	}
	return remote.withRefs(tags)
}

func (remote *Remote) Branches() *Remote {
	var branches []*Ref
	for _, ref := range remote.Refs {
		if !strings.HasPrefix(ref.Name, "refs/heads/") {
			continue // skip non-branches
		}
		branches = append(branches, ref)
	}
	return remote.withRefs(branches)
}

func (remote *Remote) Filter(patterns []string) *Remote {
	if len(patterns) == 0 {
		return remote
	}
	var refs []*Ref
	for _, ref := range remote.Refs {
		matched := false
		for _, pattern := range patterns {
			ok, _ := gitTailMatch(pattern, ref.Name)
			if ok {
				matched = true
				break
			}
		}
		if matched {
			refs = append(refs, ref)
		}
	}
	return remote.withRefs(refs)
}

func (remote *Remote) ShortNames() []string {
	names := make([]string, len(remote.Refs))
	for i, ref := range remote.Refs {
		names[i] = ref.ShortName()
	}
	return names
}

func (remote *Remote) Get(name string) (result *Ref) {
	// TODO: maybe use remote.lookup instead ?
	return remote.RefMap[name]
}

// Add assumes remote.Get(name) returns nil
func (remote *Remote) Add(name, sha string) error {
	ref := &Ref{Name: name, SHA: sha}
	remote.RefMap[name] = ref
	j := len(remote.Refs)
	remote.Refs = append(remote.Refs, ref)
	// insert into sorted slice
	i := sort.Search(len(remote.Refs), func(i int) bool {
		return remote.Refs[i].Name >= name
	})
	if i != j {
		remote.Refs[i], remote.Refs[j] = remote.Refs[j], remote.Refs[i]
	}
	return nil
}

// func (remote *Remote) resolveHead(target string) (string, bool) {
// 	return "", false
// }

// Lookup looks up a ref by name, simulating git-checkout semantics.
// It handles full refs, partial refs, commits, symrefs, HEAD resolution, etc.
func (remote *Remote) Lookup(target string) (result *Ref, _ error) {
	match, err := remote.lookup(target)
	if err != nil {
		return nil, err
	}
	if match == nil {
		return nil, fmt.Errorf("[ls-remote] repository does not contain ref %q", target)
	}
	return match, nil
}

func (remote *Remote) lookup(target string) (match *Ref, err error) {
	isHead := target == "HEAD"
	if isHead && remote.Head != nil && remote.Head.Name != "" {
		// resolve HEAD to a specific ref
		target = remote.Head.Name
	}
	if IsCommitSHA(target) {
		return &Ref{SHA: target}, nil
	}

	// simulate git-checkout semantics, and make sure to select exactly the right ref

	targetRef := remote.RefMap[target]
	partialRef := remote.RefMap["refs/"+strings.TrimPrefix(target, "refs/")]
	headRef := remote.RefMap["refs/heads/"+strings.TrimPrefix(target, "refs/heads/")]
	tagRefName := "refs/tags/" + strings.TrimPrefix(target, "refs/tags/")
	tagRef := remote.RefMap[tagRefName]
	peeledTagRef := remote.RefMap[tagRefName+"^{}"]
	if peeledTagRef != nil {
		peeledTagRef.Name = tagRefName
	}

	// git-checkout prefers branches in case of ambiguity
	for _, match = range []*Ref{targetRef, partialRef, headRef, tagRef, peeledTagRef} {
		if match != nil {
			break
		}
	}
	if match == nil {
		return nil, nil
	}

	if !IsCommitSHA(match.SHA) {
		return nil, fmt.Errorf("invalid commit sha %q for %q", match.SHA, match.Name)
	}

	// clone the match to avoid weirdly mutating later
	clone := *match
	match = &clone

	// resolve symrefs to get the right ref result
	if ref, ok := remote.Symrefs[match.Name]; ok {
		match.Name = ref
	}

	if isHead && remote.Head != nil && remote.Head.SHA != "" {
		match.SHA = remote.Head.SHA
	}

	return match, nil
}
