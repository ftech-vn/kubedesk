# The kubedesk.cloud website: static files served by an unprivileged nginx on :8080.
FROM nginxinc/nginx-unprivileged:1.28-alpine

COPY nginx.conf /etc/nginx/conf.d/default.conf
COPY *.html *.css *.js robots.txt sitemap.xml site.webmanifest /usr/share/nginx/html/
COPY assets /usr/share/nginx/html/assets

EXPOSE 8080
