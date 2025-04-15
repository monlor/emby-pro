#!/bin/bash

# Default values
DEFAULT_CRON="0 2 * * *"
DEFAULT_FLATTEN_MODE="False"
DEFAULT_SUBTITLE="False"
DEFAULT_IMAGE="False"
DEFAULT_NFO="False"
DEFAULT_MODE="AlistURL"
DEFAULT_OVERWRITE="False"
DEFAULT_SYNC_SERVER="True"
DEFAULT_SYNC_IGNORE="\\.(nfo|jpg)$"
DEFAULT_MAX_WORKERS=50
DEFAULT_MAX_DOWNLOADERS=5
DEFAULT_WAIT_TIME=0

CONFIG_FILE="/config/config.yaml"

# Required environment variables
if [ -z "$ALIST_URL" ]; then
    echo "Error: ALIST_URL environment variable is required"
    exit 1
fi

if [ -z "$ALIST_USERNAME" ]; then
    echo "Error: ALIST_USERNAME environment variable is required"
    exit 1
fi

if [ -z "$ALIST_PASSWORD" ]; then
    echo "Error: ALIST_PASSWORD environment variable is required"
    exit 1
fi

if [ -z "$ALIST_TOKEN" ]; then
    echo "Error: ALIST_TOKEN environment variable is required"
    exit 1
fi

# Optional environment variables with defaults
CRON=${CRON:-$DEFAULT_CRON}
FLATTEN_MODE=${FLATTEN_MODE:-$DEFAULT_FLATTEN_MODE}
SUBTITLE=${SUBTITLE:-$DEFAULT_SUBTITLE}
IMAGE=${IMAGE:-$DEFAULT_IMAGE}
NFO=${NFO:-$DEFAULT_NFO}
MODE=${MODE:-$DEFAULT_MODE}
OVERWRITE=${OVERWRITE:-$DEFAULT_OVERWRITE}
SYNC_SERVER=${SYNC_SERVER:-$DEFAULT_SYNC_SERVER}
SYNC_IGNORE=${SYNC_IGNORE:-$DEFAULT_SYNC_IGNORE}
MAX_WORKERS=${MAX_WORKERS:-$DEFAULT_MAX_WORKERS}
MAX_DOWNLOADERS=${MAX_DOWNLOADERS:-$DEFAULT_MAX_DOWNLOADERS}
WAIT_TIME=${WAIT_TIME:-$DEFAULT_WAIT_TIME}

# Create config.yaml
cat > $CONFIG_FILE << EOF
Settings:
  DEV: False

Alist2StrmList:
EOF

# Process source paths if provided
if [ -n "$SOURCE_PATHS" ]; then
    IFS=',' read -ra PATHS <<< "$SOURCE_PATHS"
    for path_pair in "${PATHS[@]}"; do
        IFS=':' read -ra parts <<< "$path_pair"
        target_subdir=${parts[0]}
        source_path=${parts[1]:-""}

        if [ -z "$target_subdir" ]; then
            id=$(basename $source_path)
        else    
            id=$target_subdir
        fi
        
        # Create target directory path
        if [ -n "$target_subdir" ]; then
            target_dir="/media/$target_subdir"
        else
            target_dir="/media/$id"
        fi

        cat >> $CONFIG_FILE << EOF
  - id: $id
    cron: $CRON
    url: $ALIST_URL
    username: $ALIST_USERNAME
    password: $ALIST_PASSWORD
    token: ${ALIST_TOKEN:-""}
    source_dir: $source_path
    target_dir: $target_dir
    flatten_mode: $FLATTEN_MODE
    subtitle: $SUBTITLE
    image: $IMAGE
    nfo: $NFO
    mode: $MODE
    overwrite: $OVERWRITE
    sync_server: $SYNC_SERVER
    sync_ignore: $SYNC_IGNORE
    max_workers: $MAX_WORKERS
    max_downloaders: $MAX_DOWNLOADERS
    wait_time: $WAIT_TIME
EOF
    done
else
    # If no source paths provided, create a single entry for root
    cat >> $CONFIG_FILE << EOF
  - id: root
    cron: $CRON
    url: $ALIST_URL
    username: $ALIST_USERNAME
    password: $ALIST_PASSWORD
    token: ${ALIST_TOKEN:-""}
    source_dir: /
    target_dir: /media
    flatten_mode: $FLATTEN_MODE
    subtitle: $SUBTITLE
    image: $IMAGE
    nfo: $NFO
    mode: $MODE
    overwrite: $OVERWRITE
    sync_server: $SYNC_SERVER
    sync_ignore: $SYNC_IGNORE
    max_workers: $MAX_WORKERS
    max_downloaders: $MAX_DOWNLOADERS
    wait_time: $WAIT_TIME
EOF
fi

python3 /app/main.py