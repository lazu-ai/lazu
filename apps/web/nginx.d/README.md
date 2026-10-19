# nginx extension templates

Builds that compile extensions in place nginx templates here, in three
folders, each file ending in `.conf.template`:

- `http.d/` — http-level configuration (maps).
- `server.d/` — server-level configuration and locations, read before the
  core's locations.
- `page.d/` — lines added to every public page location.

The image renders them to `/etc/nginx/conf.d/lazu/` at start. Only variables
named `LAZU_NGINX_*` are substituted; nginx's own `$variables` are kept. The
public tree has none.
