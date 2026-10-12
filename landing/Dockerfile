FROM nginx:1.27-alpine
COPY nginx.conf /etc/nginx/conf.d/default.conf
COPY --chown=nginx:nginx . /usr/share/nginx/html
RUN rm -rf /usr/share/nginx/html/scripts /usr/share/nginx/html/guide-src /usr/share/nginx/html/docs-src /usr/share/nginx/html/README.md /usr/share/nginx/html/Dockerfile /usr/share/nginx/html/nginx.conf /usr/share/nginx/html/.dockerignore
