#!/usr/bin/env bash
DIR=/root/tollgate-vm
case "$1" in
  kill) pkill -f "tollgate.qemu" 2>/dev/null; sleep 1; echo killed; exit 0;;
  boot)
    pkill -f "tollgate.qemu" 2>/dev/null; sleep 1
    setsid qemu-system-x86_64 -machine pc -cpu max -m 512M \
      -drive file="$DIR/openwrt-25.12.0-x86-64-combined.img",format=raw,if=virtio,snapshot=on \
      -netdev user,id=net0,hostfwd=tcp:127.0.0.1:2222-:22 \
      -device virtio-net-pci,netdev=net0 \
      -netdev user,id=net1 \
      -device virtio-net-pci,netdev=net1 \
      -display none -serial pty -monitor none -daemonize \
      -pidfile "$DIR/vm.pid" -name tollgate.qemu 2>"$DIR/vm.err"
    sleep 2
    PTY=$(ls -t /dev/pts/ | grep -E "^[0-9]+$" | head -1)
    echo "/dev/pts/$PTY" > "$DIR/vm.pty"
    echo "booted pty=/dev/pts/$PTY pid=$(cat $DIR/vm.pid 2>/dev/null)"
    ;;
esac
