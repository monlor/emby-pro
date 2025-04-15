#!/bin/bash

# Default values
DEFAULT_PORT=9000
DEFAULT_MEDIA_SERVER_TYPE="Emby"
DEFAULT_ACCESS_LOGGER_CONSOLE="True"
DEFAULT_ACCESS_LOGGER_FILE="False"
DEFAULT_SERVICE_LOGGER_CONSOLE="True"
DEFAULT_SERVICE_LOGGER_FILE="True"
DEFAULT_WEB_ENABLE="True"
DEFAULT_WEB_CUSTOM="False"
DEFAULT_WEB_INDEX="False"
DEFAULT_WEB_CRX="True"
DEFAULT_WEB_ACTOR_PLUS="True"
DEFAULT_WEB_FANART_SHOW="False"
DEFAULT_WEB_EXTERNAL_PLAYER_URL="True"
DEFAULT_WEB_DANMAKU="True"
DEFAULT_WEB_VIDEO_TOGETHER="True"
DEFAULT_CLIENT_FILTER_ENABLE="False"
DEFAULT_CLIENT_FILTER_MODE="BlackList"
DEFAULT_HTTP_STRM_ENABLE="True"
DEFAULT_HTTP_STRM_TRANSCODE="False"
DEFAULT_ALIST_STRM_ENABLE="False"
DEFAULT_ALIST_STRM_TRANSCODE="True"
DEFAULT_ALIST_STRM_RAW_URL="False"
DEFAULT_SUBTITLE_ENABLE="True"
DEFAULT_SUBTITLE_SRT2ASS="True"

CONFIG_FILE="/config/config.yaml"

# Required environment variables
if [ -z "$MEDIA_SERVER_ADDR" ]; then
    echo "Error: MEDIA_SERVER_ADDR environment variable is required"
    exit 1
fi

if [ -z "$MEDIA_SERVER_AUTH" ]; then
    echo "Error: MEDIA_SERVER_AUTH environment variable is required"
    exit 1
fi

# Optional environment variables with defaults
PORT=${PORT:-$DEFAULT_PORT}
MEDIA_SERVER_TYPE=${MEDIA_SERVER_TYPE:-$DEFAULT_MEDIA_SERVER_TYPE}
ACCESS_LOGGER_CONSOLE=${ACCESS_LOGGER_CONSOLE:-$DEFAULT_ACCESS_LOGGER_CONSOLE}
ACCESS_LOGGER_FILE=${ACCESS_LOGGER_FILE:-$DEFAULT_ACCESS_LOGGER_FILE}
SERVICE_LOGGER_CONSOLE=${SERVICE_LOGGER_CONSOLE:-$DEFAULT_SERVICE_LOGGER_CONSOLE}
SERVICE_LOGGER_FILE=${SERVICE_LOGGER_FILE:-$DEFAULT_SERVICE_LOGGER_FILE}
WEB_ENABLE=${WEB_ENABLE:-$DEFAULT_WEB_ENABLE}
WEB_CUSTOM=${WEB_CUSTOM:-$DEFAULT_WEB_CUSTOM}
WEB_INDEX=${WEB_INDEX:-$DEFAULT_WEB_INDEX}
WEB_CRX=${WEB_CRX:-$DEFAULT_WEB_CRX}
WEB_ACTOR_PLUS=${WEB_ACTOR_PLUS:-$DEFAULT_WEB_ACTOR_PLUS}
WEB_FANART_SHOW=${WEB_FANART_SHOW:-$DEFAULT_WEB_FANART_SHOW}
WEB_EXTERNAL_PLAYER_URL=${WEB_EXTERNAL_PLAYER_URL:-$DEFAULT_WEB_EXTERNAL_PLAYER_URL}
WEB_DANMAKU=${WEB_DANMAKU:-$DEFAULT_WEB_DANMAKU}
WEB_VIDEO_TOGETHER=${WEB_VIDEO_TOGETHER:-$DEFAULT_WEB_VIDEO_TOGETHER}
CLIENT_FILTER_ENABLE=${CLIENT_FILTER_ENABLE:-$DEFAULT_CLIENT_FILTER_ENABLE}
CLIENT_FILTER_MODE=${CLIENT_FILTER_MODE:-$DEFAULT_CLIENT_FILTER_MODE}
HTTP_STRM_ENABLE=${HTTP_STRM_ENABLE:-$DEFAULT_HTTP_STRM_ENABLE}
HTTP_STRM_TRANSCODE=${HTTP_STRM_TRANSCODE:-$DEFAULT_HTTP_STRM_TRANSCODE}
ALIST_STRM_ENABLE=${ALIST_STRM_ENABLE:-$DEFAULT_ALIST_STRM_ENABLE}
ALIST_STRM_TRANSCODE=${ALIST_STRM_TRANSCODE:-$DEFAULT_ALIST_STRM_TRANSCODE}
ALIST_STRM_RAW_URL=${ALIST_STRM_RAW_URL:-$DEFAULT_ALIST_STRM_RAW_URL}
SUBTITLE_ENABLE=${SUBTITLE_ENABLE:-$DEFAULT_SUBTITLE_ENABLE}
SUBTITLE_SRT2ASS=${SUBTITLE_SRT2ASS:-$DEFAULT_SUBTITLE_SRT2ASS}

# Create config.yaml
cat > $CONFIG_FILE << EOF
Port: $PORT

MediaServer:
  Type: $MEDIA_SERVER_TYPE
  ADDR: $MEDIA_SERVER_ADDR
  AUTH: $MEDIA_SERVER_AUTH

Logger:
  AccessLogger:
    Console: $ACCESS_LOGGER_CONSOLE
    File: $ACCESS_LOGGER_FILE
  ServiceLogger:
    Console: $SERVICE_LOGGER_CONSOLE
    File: $SERVICE_LOGGER_FILE

Web:
  Enable: $WEB_ENABLE
  Custom: $WEB_CUSTOM
  Index: $WEB_INDEX
  Head: |                                  
    <script src="/MediaWarp/custom/emby-front-end-mod/actor-plus.js"></script>
    <script src="/MediaWarp/custom/emby-front-end-mod/emby-swiper.js"></script>
    <script src="/MediaWarp/custom/emby-front-end-mod/emby-tab.js"></script>
    <script src="/MediaWarp/custom/emby-front-end-mod/fanart-show.js"></script>
    <script src="/MediaWarp/custom/emby-front-end-mod/playbackRate.js"></script>

  Crx: $WEB_CRX
  ActorPlus: $WEB_ACTOR_PLUS
  FanartShow: $WEB_FANART_SHOW
  ExternalPlayerUrl: $WEB_EXTERNAL_PLAYER_URL
  Danmaku: $WEB_DANMAKU
  VideoTogether: $WEB_VIDEO_TOGETHER

ClientFilter:
  Enable: $CLIENT_FILTER_ENABLE
  Mode: $CLIENT_FILTER_MODE
  ClientList:
    - Fileball
    - Infuse

HTTPStrm:
  Enable: $HTTP_STRM_ENABLE
  TransCode: $HTTP_STRM_TRANSCODE
  PrefixList:
    - /media

AlistStrm:
  Enable: $ALIST_STRM_ENABLE
  TransCode: $ALIST_STRM_TRANSCODE
  RawURL: $ALIST_STRM_RAW_URL
  List:
    - ADDR: $ALIST_ADDR
      Username: ${ALIST_USERNAME:-""}
      Password: ${ALIST_PASSWORD:-""}
      Token: ${ALIST_TOKEN:-""}
      PrefixList:
        - /media

Subtitle:
  Enable: $SUBTITLE_ENABLE
  SRT2ASS: $SUBTITLE_SRT2ASS
  ASSStyle:
    - "Format: Name, Fontname, Fontsize, PrimaryColour, SecondaryColour, OutlineColour, BackColour, Bold, Italic, Underline, StrikeOut, ScaleX, ScaleY, Spacing, Angle, BorderStyle, Outline, Shadow, Alignment, MarginL, MarginR, MarginV, Encoding"
    - "Style: Default,楷体,20,&H03FFFFFF,&H00FFFFFF,&H00000000,&H02000000,-1,0,0,0,100,100,0,0,1,1,0,2,10,10,10,1"
EOF

/MediaWarp