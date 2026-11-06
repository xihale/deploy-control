# deploy.xeed.ink — deployment control panel (deployd).
# Append to /etc/caddy/Caddyfile. deployd listens on 127.0.0.1:8795 and
# enforces its own bearer-token auth; webhook HMAC covers /webhook/github/*.
deploy.xeed.ink {
	reverse_proxy 127.0.0.1:8795
}
