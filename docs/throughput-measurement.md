Previously I had issues with `ndsctl`, because it wasn't measuring the data consumption of the sessions that it managed accurately. Infact there didn't seem to be any correlation between the actual data consumption and the output of `ndsctl json`. At the time ndsctl was migrating from `iptables` to `nftables`. I wonder if that was part of the problem.


Now I'm re-visiting this issue more than a year later to see if I can reproduce it. I established a fresh captive portal session and ran:

```
c03rad0r@c03rad0r-ThinkPad-X280:~$ yes "this is a test to find out if the data measurement is working correctly on a tollgate" | pv | nc damus.io 443
HTTP/1.1 400 Bad Request
Server: cloudflare
Date: Mon, 15 Dec 2025 04:32:41 GMT
Content-Type: text/html
Content-Length: 155
Connection: close
CF-RAY: -

<html>
<head><title>400 Bad Request</title></head>
<body>
<center><h1>400 Bad Request</h1></center>
<hr><center>cloudflare</center>
</body>
</html>
9.57MiB 0:00:30 [ 323KiB/s] [    
```

After loading the almost `10 MiB`, I had the following output in `ndsctl json`

```
root@OpenWrt:~# ndsctl json
{
"client_length": 1,
"clients":{
"00:e0:4c:68:3d:2d":{
"id":1,
"ip":"172.16.1.106",
"mac":"00:e0:4c:68:3d:2d",
"added":0,
"active":1765773194,
"duration":0,
"token":"7a20c37a",
"state":"Authenticated",
"downloaded":14396,
"avg_down_speed":0.00,
"uploaded":10825,
"avg_up_speed":0.00
}
}
}

root@OpenWrt:~# ndsctl status
==================
NoDogSplash Status
====
Version: 5.0.2
Uptime: 22h 18m 46s
Gateway Name: TollGate-ISTX Portal
Managed interface: br-lan
Managed IP range: 0.0.0.0/0
Server listening: http://172.16.1.1:2050
Binauth: Disabled
Preauth: Disabled
Client Check Interval: 600s
Preauth Idle Timeout: 30m 0sm
Auth Idle Timeout: 2h 0m 0s
Session Timeout: 20h 0m 0s
Session Timeout: 20h 0m 0s
Block after Session timed out: no
Traffic control: no
Total download: 16582 kByte; avg: 1.65 kbit/s
Total upload: 11861 kByte; avg: 1.18 kbit/s
====
Client authentications since start: 1
Current clients: 1

Client 0
  IP: 172.16.1.106 MAC: 00:e0:4c:68:3d:2d
  Last Activity: Mon Dec 15 04:45:05 2025 (0s ago)
  Session Start: -
  Session End:   -
  Token: 7a20c37a
  State: Authenticated
  Download: 15421 kByte; avg: 0.00 kbit/s
  Upload:   11509 kByte; avg: 0.00 kbit/s

====
Blocked MAC addresses: none
Allowed MAC addresses: N/A
Trusted MAC addresses: none
========



```

If `downloaded` and `uploaded` in the output of `ndsctl json` are tracking `KiB`, the `uploaded` value is quite close to the value that `pv` measured. Hoever, the `downloaded` value says that the session consumed `14396 MiB`.

Whats the right way to interpret these values? What other methods are there to verify that captive portal is or isn't measuring data correctly? 

