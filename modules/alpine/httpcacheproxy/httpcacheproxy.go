package httpcacheproxy

import (
	"bytes"
	"encoding/gob"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/zeebo/xxh3"
)

type CacheItem struct {
	Response   []byte
	Header     http.Header
	Expiration time.Time
	ETag       string
	LastMod    string
	StatusCode int
}
type cacheMap map[uint64]CacheItem

type CacheOpts struct {
	TTL      time.Duration
	LoadPath string
	// Caches all requests via TTL
	GottaCacheEmAll bool
	// if a client request or a cached entry has an ETag or Last-Modified header, ask the server if the content has been modified
	FreshnessOverTTL bool
}

type Cache struct {
	items            cacheMap
	mutex            sync.RWMutex
	TTL              time.Duration
	gottaCacheEmAll  bool
	freshnessOverTTL bool
}

func (c *Cache) Save(path string) error {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	log.Println("YOLOOOOO saving cache to", path, len(c.items))
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer func() {
		f.Close()
		log.Println("Closed", path)
		p, err := ioutil.ReadFile(path)
		log.Println("checking reopen", err)
		var x cacheMap
		err = gob.NewDecoder(bytes.NewReader(p)).Decode(&x)
		log.Println("checking decode", err)
		log.Println("x", len(x))
	}()
	return gob.NewEncoder(f).Encode(&c.items)
}

func NewCache(opts CacheOpts) (*Cache, error) {
	if opts.GottaCacheEmAll && opts.FreshnessOverTTL {
		return nil, fmt.Errorf("GottaCacheEmAll and FreshnessOverTTL cannot both be true as they are mutually exclusive")
	}

	items := make(cacheMap)
	if opts.LoadPath != "" {
		f, err := os.Open(opts.LoadPath)
		if err == nil {
			defer f.Close()
			if err := gob.NewDecoder(f).Decode(&items); err != nil && !errors.Is(err, io.EOF) {
				return nil, fmt.Errorf("gob decoding: %w", err)
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("opening %s: %w", opts.LoadPath, err)
		}
	}

	log.Println("NewCache", len(items))

	return &Cache{
		items:            items,
		TTL:              opts.TTL,
		gottaCacheEmAll:  opts.GottaCacheEmAll,
		freshnessOverTTL: opts.FreshnessOverTTL,
	}, nil
}

func (c *Cache) Get(key uint64) (CacheItem, bool) {
	c.mutex.RLock()
	defer c.mutex.RUnlock()
	item, found := c.items[key]
	if !found || time.Now().After(item.Expiration) {
		return CacheItem{}, false
	}
	return item, true
}

func (c *Cache) Set(key uint64, response io.Reader, header http.Header, statusCode int) {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	if !c.gottaCacheEmAll {
		// Honor Cache-Control headers
		cacheControl := header.Get("Cache-Control")
		if strings.Contains(cacheControl, "no-store") {
			return
		}
		if strings.Contains(cacheControl, "private") {
			return
		}
	}

	var body []byte
	body, _ = io.ReadAll(response)

	expiration := time.Now().Add(c.TTL)
	if !c.gottaCacheEmAll {
		if maxAgeIndex := strings.Index(header.Get("Cache-Control"), "max-age="); maxAgeIndex != -1 {
			var maxAge int
			if _, err := fmt.Sscanf(header.Get("Cache-Control")[maxAgeIndex:], "max-age=%d", &maxAge); err == nil {
				expiration = time.Now().Add(time.Duration(maxAge) * time.Second)
			}
		}
	}

	c.items[key] = CacheItem{
		Response:   body,
		Header:     header,
		Expiration: expiration,
		ETag:       header.Get("ETag"),
		LastMod:    header.Get("Last-Modified"),
		StatusCode: statusCode,
	}
}

func hashRequest(req *http.Request) uint64 {
	baseKey := req.Method + req.URL.String()
	varyHeaders := req.Header["Vary"]
	if len(varyHeaders) > 0 {
		for _, header := range varyHeaders {
			baseKey += req.Header.Get(header)
		}
	}
	return xxh3.HashString(baseKey)
}

func writeHeader(w http.ResponseWriter, statusCode int, header http.Header) {
	for k, v := range header {
		for _, hv := range v {
			w.Header().Add(k, hv)
		}
	}
	w.WriteHeader(statusCode)
}

func writeResponse(w http.ResponseWriter, statusCode int, header http.Header, body []byte) {
	writeHeader(w, statusCode, header)
	w.Write(body)
}

func writeResponseWithBody(w http.ResponseWriter, statusCode int, header http.Header, body io.Reader) {
	writeHeader(w, statusCode, header)
	io.Copy(w, body)
}

func proxyHandler(cache *Cache) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		log.Println("NEW request", r)
		if !cache.gottaCacheEmAll && r.Method != "GET" {
			// TODO: parameterize Client
			resp, err := http.DefaultClient.Do(r)
			if err != nil {
				http.Error(w, "Proxy error", http.StatusBadGateway)
				return
			}
			defer resp.Body.Close()
			writeResponseWithBody(w, resp.StatusCode, resp.Header, resp.Body)
			return
		}
		key := hashRequest(r)
		cached, found := cache.Get(key)
		clientETag := r.Header.Get("If-None-Match")
		clientLastMod := r.Header.Get("If-Modified-Since")
		canCheckFreshness := cache.freshnessOverTTL && clientETag == "" && clientLastMod == ""
		shouldUseCached := found && (cache.gottaCacheEmAll || canCheckFreshness)
		validateCache := found && canCheckFreshness

		log.Println("found?", found)
		if found {
			if canCheckFreshness {
				// Use cached headers for freshness check
				if cached.ETag != "" {
					r.Header.Set("If-None-Match", cached.ETag)
					shouldUseCached = false
					validateCache = true
				}
				if cached.LastMod != "" {
					r.Header.Set("If-Modified-Since", cached.LastMod)
					shouldUseCached = false
					validateCache = true
				}
			}

			log.Println("shouldUseCached?", shouldUseCached)
			if shouldUseCached {
				if (clientETag != "" && cached.ETag == clientETag) || (clientLastMod != "" && cached.LastMod == clientLastMod) {
					w.WriteHeader(http.StatusNotModified)
					return
				}
				writeResponse(w, cached.StatusCode, cached.Header, cached.Response)
				return
			}
		}

		// TODO: parameterize Client
		resp, err := http.DefaultClient.Do(r)
		if err != nil {
			http.Error(w, "Proxy error", http.StatusBadGateway)
			return
		}
		defer resp.Body.Close()

		if validateCache && resp.StatusCode == http.StatusNotModified {
			writeResponse(w, cached.StatusCode, cached.Header, cached.Response)
			return
		}

		pipeR, pipeW := io.Pipe()
		reader := io.TeeReader(resp.Body, pipeW)
		go func() {
			cache.Set(key, pipeR, resp.Header, resp.StatusCode)
			for k, v := range resp.Header {
				for _, hv := range v {
					w.Header().Add(k, hv)
				}
			}
		}()
		w.WriteHeader(resp.StatusCode)
		_, err = io.Copy(w, reader)
		pipeW.CloseWithError(err)
		log.Println("end of request", r)
	}
}

func New(addr, cacheFile string, shutdownCh chan error) (*http.Server, error) {
	cache, err := NewCache(CacheOpts{TTL: 24 * time.Hour, LoadPath: cacheFile, GottaCacheEmAll: true, FreshnessOverTTL: false})
	if err != nil {
		return nil, fmt.Errorf("Error creating http proxy cache backend: %w", err)
	}
	s := &http.Server{Addr: addr, Handler: proxyHandler(cache)}
	s.RegisterOnShutdown(func() {
		log.Println("Server shutting down")
		err := cache.Save(cacheFile)
		log.Println("Done shutting down", err)
		shutdownCh <- err
	})
	log.Println("Server created")
	return s, nil
}
