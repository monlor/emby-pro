FROM akimio/autofilm:1.3.2

RUN apk add --no-cache bash

COPY --chmod=755 entrypoint.sh /entrypoint.sh

ENTRYPOINT ["/entrypoint.sh"]