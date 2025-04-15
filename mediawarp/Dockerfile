FROM akimio/mediawarp:0.1.0

RUN apk add --no-cache bash

COPY --chmod=755 entrypoint.sh /entrypoint.sh

ENTRYPOINT ["/entrypoint.sh"]