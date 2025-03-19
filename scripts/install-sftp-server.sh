#!/bin/bash

if [ ! -f /usr/lib/sftp-server ]; then
    ln -s /usr/lib/ssh/sftp-server /usr/lib/sftp-server
fi

uci set dropbear.@dropbear[0].PasswordAuth='on'
uci set dropbear.@dropbear[0].RootPasswordAuth='on'
uci set dropbear.@dropbear[0].Port='22'
uci commit dropbear

/etc/init.d/dropbear restart