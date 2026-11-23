# deploy.xeed.ink — deployment control panel (deployd).
# Append to /etc/caddy/Caddyfile. deployd listens on 127.0.0.1:8795 and
# enforces its own bearer-token auth; webhook HMAC covers /webhook/github/*.
# If fronted by an upstream Caddy/reverse proxy (e.g. `g` at 10.1.0.2), also add
# `servers { trusted_proxies static 10.1.0.2/32 }` to Caddy's global options so
# `request.client_ip` reflects real visitor IPs for analytics & bot blocking.
deploy.xeed.ink {
	import bot_shield
	reverse_proxy 127.0.0.1:8795
}

# To feed deployd analytics, enable Caddy JSON access logging on your sites
# (or inside a shared Caddy snippet imported by each site block):
#
# (site_log) {
# 	log {
# 		output file /var/log/caddy/access.log {
# 			roll_size 100mb
# 			roll_keep 5
# 		}
# 		format json
# 	}
# }
