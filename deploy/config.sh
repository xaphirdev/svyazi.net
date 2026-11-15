#!/bin/sh

env_value() { awk -F= -v key="$1" '$1==key {sub(/^[^=]*=/, ""); result=$0} END {print result}' .env; }

set_default() {
    # Fixed-string key matching also supports the requested dotted key.
    value=$(env_value "$1")
    if [ -z "$value" ]; then
        awk -F= -v key="$1" '$1!=key' .env > .local/env.tmp
        cat .local/env.tmp > .env
        printf '%s=%s\n' "$1" "$2" >> .env
        rm -f .local/env.tmp
    fi
}

prepare_env() {
    umask 077
    mkdir -p .local/credentials
    touch .env
    sed -i 's/\r$//' .env
    chmod 600 .env
    for key in DB_ROOT_PASSWORD DB_PASSWORD; do
        if [ -z "$(env_value "$key")" ]; then
            secret=$(od -An -N24 -tx1 /dev/urandom | tr -d ' \n')
            set_default "$key" "$secret"
        fi
    done
    sed -i '/^SVYAZI_NET_ADMIN_PASSWORD=/d' .env
    set_default admin.svyazi.net admin
    set_default APP_PORT 8000
    set_default SEED_INITIAL_DATA 1
    APP_PORT=$(env_value APP_PORT)
    case "$APP_PORT" in ''|*[!0-9]*|0*) die 'APP_PORT должен быть числом 1–65535 без ведущих нулей.' ;; esac
    [ "${#APP_PORT}" -le 5 ] && [ "$APP_PORT" -le 65535 ] && [ "$APP_PORT" -ne 3306 ] || die 'Некорректный APP_PORT или конфликт с портом БД.'
    chmod 700 .local/credentials
    grep '^admin\.svyazi\.net=' .env > .local/app.env
    chmod 600 .local/app.env
    export APP_PORT
    export APP_BIND=0.0.0.0
}
