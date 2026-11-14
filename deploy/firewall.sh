#!/bin/sh
set -eu

apply_rules() {
    . /etc/svyazi.net/firewall.conf
    # Remove only database openings created by previous versions of this project.
    while iptables -w -C INPUT -p tcp --dport 3306 -m comment --comment svyazi.net-published -j ACCEPT 2>/dev/null; do
        iptables -w -D INPUT -p tcp --dport 3306 -m comment --comment svyazi.net-published -j ACCEPT
    done
    if iptables -w -nL DOCKER-USER >/dev/null 2>&1; then
        while iptables -w -C DOCKER-USER -p tcp -m conntrack --ctstate DNAT --ctorigdstport 3306 -m comment --comment svyazi.net-published -j ACCEPT 2>/dev/null; do
            iptables -w -D DOCKER-USER -p tcp -m conntrack --ctstate DNAT --ctorigdstport 3306 -m comment --comment svyazi.net-published -j ACCEPT
        done
    fi
    for port in "$APP_PORT"; do
        iptables -w -C INPUT -p tcp --dport "$port" -m comment --comment svyazi.net-published -j ACCEPT 2>/dev/null ||
            iptables -w -I INPUT 1 -p tcp --dport "$port" -m comment --comment svyazi.net-published -j ACCEPT
        if iptables -w -nL DOCKER-USER >/dev/null 2>&1; then
            iptables -w -C DOCKER-USER -p tcp -m conntrack --ctstate DNAT --ctorigdstport "$port" -m comment --comment svyazi.net-published -j ACCEPT 2>/dev/null ||
                iptables -w -I DOCKER-USER 1 -p tcp -m conntrack --ctstate DNAT --ctorigdstport "$port" -m comment --comment svyazi.net-published -j ACCEPT
        fi
    done
}

if [ "${1:-}" = --apply ]; then apply_rules; exit 0; fi

APP_PORT=${1:?Укажите порт приложения}
case "$APP_PORT" in ''|*[!0-9]*) echo 'Некорректный порт' >&2; exit 1 ;; esac
[ "$APP_PORT" -ge 1 ] && [ "$APP_PORT" -le 65535 ] || exit 1
install -m 0755 -d /etc/svyazi.net /usr/local/lib/svyazi.net
printf 'APP_PORT=%s\n' "$APP_PORT" > /etc/svyazi.net/firewall.conf
install -m 0755 deploy/firewall.sh /usr/local/lib/svyazi.net/firewall.sh

# INPUT rules alone do not cover Docker's forwarded traffic.
if command -v ufw >/dev/null 2>&1 && LC_ALL=C ufw status | grep -q '^Status: active'; then
    if LC_ALL=C ufw status | grep -Eq '^3306/tcp[[:space:]]+ALLOW'; then
        ufw --force delete allow 3306/tcp
    fi
    ufw allow "$APP_PORT/tcp"
fi
if command -v firewall-cmd >/dev/null 2>&1 && firewall-cmd --state >/dev/null 2>&1; then
    if firewall-cmd --permanent --query-port=3306/tcp >/dev/null; then
        firewall-cmd --permanent --remove-port=3306/tcp
    fi
    if firewall-cmd --query-port=3306/tcp >/dev/null; then
        firewall-cmd --remove-port=3306/tcp
    fi
    for port in "$APP_PORT"; do
        firewall-cmd --permanent --add-port="$port/tcp"
        firewall-cmd --add-port="$port/tcp"
    done
fi
. ./deploy/common.sh
detect_init
case "$INIT_SYSTEM" in
    systemd)
        install -m 0644 deploy/svyazi.net-firewall.service /etc/systemd/system/svyazi.net-firewall.service
        systemctl daemon-reload
        systemctl enable svyazi.net-firewall.service
        systemctl restart svyazi.net-firewall.service
        ;;
    openrc)
        install -m 0755 deploy/svyazi.net-firewall.openrc /etc/init.d/svyazi.net-firewall
        rc-update add svyazi.net-firewall default
        rc-service svyazi.net-firewall restart
        ;;
    sysv)
        install -m 0755 deploy/svyazi.net-firewall.init /etc/init.d/svyazi.net-firewall
        if command -v update-rc.d >/dev/null 2>&1; then update-rc.d svyazi.net-firewall defaults; fi
        service svyazi.net-firewall restart
        ;;
    none)
        apply_rules
        printf '[svyazi.net] Правила применены. Для восстановления после перезагрузки нужен менеджер служб.\n'
        ;;
esac
