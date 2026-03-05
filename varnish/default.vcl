vcl 4.1;

backend github {
    .host = "github.com";
    // varnish:stable OSS does not include HTTPS backend support by default,
    // so upstream traffic remains HTTP here.
    .port = "80";
    .connect_timeout = 5s;
    .first_byte_timeout = 300s;
    .between_bytes_timeout = 300s;
}

sub vcl_recv {
    if (req.url !~ "^/github\.com/") {
        return (synth(404, "unsupported git host (github.com only)"));
    }

    set req.backend_hint = github;
    set req.http.host = "github.com";
    set req.url = regsub(req.url, "^/github\.com", "");

    if (req.method != "GET" && req.method != "HEAD") {
        return (pass);
    }

    if (req.url ~ "^/.+\.git/info/refs") {
        if (req.url !~ "(\?|&)service=git-upload-pack(&|$)") {
            return (pass);
        }
        set req.url = regsub(req.url, "\?.*$", "") + "?service=git-upload-pack";
    }

    return (hash);
}

sub vcl_backend_response {
    set beresp.do_stream = true;

    if (bereq.url ~ "^/.+\.git/info/refs(\?|$)") {
        set beresp.ttl = 1h;
        set beresp.grace = 24h;
    } else if (bereq.url ~ "^/.+\.git/(objects/info/packs|objects/pack/.*\.(pack|idx))(\?|$)") {
        set beresp.ttl = 24h;
        set beresp.grace = 72h;
    } else {
        set beresp.ttl = 10m;
        set beresp.grace = 1h;
    }

    if (beresp.status >= 500) {
        set beresp.uncacheable = true;
        return (deliver);
    }

    return (deliver);
}

sub vcl_deliver {
    if (obj.hits > 0) {
        set resp.http.X-Varnish-Cache = "HIT";
    } else {
        set resp.http.X-Varnish-Cache = "MISS";
    }
}
