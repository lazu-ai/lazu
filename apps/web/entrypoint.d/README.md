# nginx entrypoint steps

Files here are copied to `/docker-entrypoint.d/` in the frontend image and run
by the nginx image's entrypoint in name order: `*.sh` are executed, `*.envsh`
are sourced (so they can export `LAZU_NGINX_*` variables for the templates in
`../nginx.d/`). The public tree has none.
