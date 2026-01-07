# Testing Data Measurement with Traffic Generation

This document outlines methods for generating network traffic to test the data-based session allotment functionality of the TollGate.

## Method 1: Using `yes`, `pv`, and `nc`

A simple way to generate a stream of data is by using the `yes` command, which repeatedly outputs a string. This can be piped to `pv` (Pipe Viewer) to monitor the data rate and then to `nc` (netcat) to send it to a server.

This method is useful for quick tests but may not provide a sustained, high-volume stream.

### Example

```bash
yes "this is a test to find out if the data measurement is working correctly on a tollgate" | pv | nc damus.io 443
```

This command sends a continuous stream of the specified string to `damus.io` on port 443. The `pv` command will show the throughput.

## Method 2: Using `dd` and `nc` (Recommended)

A more robust and standard method for generating a high-volume, continuous data stream on Linux-based systems is to use `dd` and `nc`. This provides a more reliable way to test if the data allotment is being correctly enforced.

### Step 1: Set up a Listening Server

On a server machine (outside the TollGate's LAN), run `netcat` in listening mode. This will accept an incoming connection and discard the data it receives.

```bash
nc -l -p 9999 > /dev/null
```
* `-l`: Listen for an incoming connection.
* `-p 9999`: Use port 9999.
* `> /dev/null`: Discard all received data.

### Step 2: Generate Traffic from the Client

On the client machine (connected to the TollGate's LAN), use `dd` to read from `/dev/zero` (an infinite stream of null characters) and pipe the output to `netcat`, connecting to your server.

```bash
dd if=/dev/zero | nc <your_server_ip> 9999
```
* `dd if=/dev/zero`: Reads an infinite stream of data.
* `| nc <your_server_ip> 9999`: Pipes the stream to your server's IP address on port 9999.

This will create a sustained data flow, allowing you to accurately verify that the `CustomerSessionTracker` closes the gate when the data limit is reached.

## Alternative: `iperf3`

For more advanced network performance testing, `iperf3` is the industry-standard tool. It provides detailed statistics on bandwidth, jitter, and packet loss.

### Server

```bash
iperf3 -s
```

### Client

```bash
iperf3 -c <your_server_ip>
```

While `iperf3` is powerful, using `dd` with `nc` is generally sufficient for testing the data allotment functionality.