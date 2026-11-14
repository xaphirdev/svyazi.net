#!/bin/sh

select_docker_repository() {
    . /etc/os-release
    case " ${ID:-} ${ID_LIKE:-} " in
        *' ubuntu '*)
            DOCKER_DISTRO=ubuntu
            DOCKER_SUITE=${UBUNTU_CODENAME:-${VERSION_CODENAME:-}}
            ;;
        *)
            DOCKER_DISTRO=debian
            if [ "${ID:-}" = debian ]; then
                DOCKER_SUITE=${VERSION_CODENAME:-}
            else
                debian_version=$(cat /etc/debian_version 2>/dev/null || true)
                case "$debian_version" in
                    13*|trixie*) DOCKER_SUITE=trixie ;;
                    12*|bookworm*) DOCKER_SUITE=bookworm ;;
                    11*|bullseye*) DOCKER_SUITE=bullseye ;;
                    *rolling*|*sid*|forky*) DOCKER_SUITE=trixie ;;
                    *) DOCKER_SUITE=${DEBIAN_CODENAME:-} ;;
                esac
            fi
            ;;
    esac
    DOCKER_DISTRO=${DOCKER_REPO_DISTRO:-$DOCKER_DISTRO}
    DOCKER_SUITE=${DOCKER_REPO_CODENAME:-$DOCKER_SUITE}
    case "$DOCKER_DISTRO" in debian|ubuntu) ;; *) die 'База репозитория Docker должна быть debian или ubuntu.' ;; esac
    case "$DOCKER_SUITE" in ''|*[!a-z0-9.-]*) die 'Не определена базовая версия системы. Задайте DOCKER_REPO_CODENAME.' ;; esac
}

add_docker_repository() {
    select_docker_repository
    # Reuse an existing official source to avoid conflicting Signed-By options.
    for source in /etc/apt/sources.list /etc/apt/sources.list.d/*.list /etc/apt/sources.list.d/*.sources; do
        [ -f "$source" ] || continue
        if grep -Eq "^[[:space:]]*(deb[[:space:]]|URIs:).*https?://download.docker.com/linux/$DOCKER_DISTRO([/[:space:]]|$)" "$source"; then
            return 0
        fi
    done
    install -m 0755 -d /etc/apt/keyrings
    curl --fail --silent --show-error --location --retry 3 "https://download.docker.com/linux/$DOCKER_DISTRO/gpg" -o /etc/apt/keyrings/docker.asc
    chmod 0644 /etc/apt/keyrings/docker.asc
    cat > /etc/apt/sources.list.d/docker.sources <<EOF
Types: deb
URIs: https://download.docker.com/linux/$DOCKER_DISTRO
Suites: $DOCKER_SUITE
Components: stable
Architectures: $(dpkg --print-architecture)
Signed-By: /etc/apt/keyrings/docker.asc
EOF
}
