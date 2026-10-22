firewall {
    name WAN_IN {
        default-action drop
    }
}
system {
    host-name ubnt
    task-scheduler {
        task reboot-lunes {
            crontab-spec "0 4 * * 1"
            executable {
                path /sbin/reboot
            }
        }
        task backup {
            interval 1d
            executable {
                path /config/scripts/backup.sh
            }
        }
        task tui-reboot-diario-0330 {
            crontab-spec "30 3 * * *"
            executable {
                path /sbin/reboot
            }
        }
    }
}
/* Warning: Do not remove the following line. */
/* === vyatta-config-version: "x" === */
