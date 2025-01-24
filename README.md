# Multi-Transfer DD Clone

&#x20;

A Go-based `dd` clone that supports **parallel transfers** with per-transfer progress bars, timers, and MB/s rates. This tool simplifies bulk copying, wiping drives, and working with raw block devices while providing a clean, visual interface for monitoring progress. There are no external dependencies and compiles on Linux and FreeBSD.

---

## Features

- **Parallel Transfers**: Supports up to 50 concurrent input-output transfers with independent configurations.
  - (If you need more than 50 parallel dd transfers, recompile with a larger MaxTransfers)
- **Detailed Progress Bars**: Visual dark-green-to-light-green progress bars for each transfer, showing:
  - Time remaining (or elapsed time after completion).
  - Real-time MB/s transfer rates.
- **Customizable Transfers**: Configure block sizes, input/output files, byte limits, and flags for each transfer.
- **Device Writing**: Supports writing to raw block devices like `/dev/sda` or `/dev/nvme0n1`.
- **BSD 3-Clause License**: Open-source and free to modify.

---

## Installation

### Prerequisites

- **Go 1.17+** (latest version recommended)

### Build the Binary

Clone the repository and build the binary:

```bash
git clone https://github.com/bjensen91/dd-multi
cd dd-multi
go build -o dd-multi dd-multi.go
```

The compiled binary `dd-multi` will be created in the current directory.

---

## Usage

### Basic Example

Run 3 parallel transfers:

```bash
./dd-multi \
  -numTransfers=3 \
  -if1=/dev/zero -of1=zero1.img -bs1=4M -size1=1073741824 -oflag1=sync \
  -if2=/dev/urandom -of2=random1.img -bs2=1M -size2=1073741824 -oflag2=sync \
  -if3=input.iso -of3=output.img -bs3=1M -size3=2147483648
```

Each transfer has its own configuration:

- **Transfer 1**: Copies 1 GB of zeros to `zero1.img`.
- **Transfer 2**: Copies 1 GB of random data to `random1.img`.
- **Transfer 3**: Copies 2 GB from `input.iso` to `output.img`.

### Progress Bar Explanation

For each transfer, you’ll see:

1. A **centered header** showing the input and output files:
   ```
   /dev/zero --> zero1.img
   ```
2. A **progress bar line**:
   ```
   00:01:23 -----------###########-----------  120.00 MB/s
   ```
   - **Left**: Countdown timer (or elapsed time after completion).
   - **Middle**: Progress bar (dark green to light green as progress increases).
   - **Right**: Transfer rate in MB/s.

### Flag Reference

- **For each transfer (1 to N):**
  - `-if{i}`: Input file/device (e.g., `/dev/zero`, `/dev/urandom`, `input.iso`).
  - `-of{i}`: Output file/device (e.g., `/dev/sda`, `output.img`).
  - `-bs{i}`: Block size (e.g., `4M`, `1M`, `512b`).
  - `-size{i}`: Total bytes to write (if no `-count{i}` is specified).
  - `-count{i}`: Number of blocks to write (overrides `-size{i}`).
  - `-skip{i}`: Skip N blocks from the input before reading.
  - `-seek{i}`: Seek N blocks on the output before writing.
  - `-conv{i}`: Conversions (e.g., `notrunc`, `none`).
  - `-oflag{i}`: Output flags (e.g., `sync`, `none`).

---

## Examples

### Wipe Two Drives in Parallel

```bash
sudo ./dd-multi \
  -numTransfers=2 \
  -if1=/dev/zero -of1=/dev/nvme0n1 -bs1=4M -size1=1000000000000 -oflag1=sync \
  -if2=/dev/zero -of2=/dev/nvme1n1 -bs2=4M -size2=1000000000000 -oflag2=sync
```

This wipes two NVMe drives with zeros in parallel.

### Copy an ISO to a USB Stick

```bash
sudo ./dd-multi \
  -numTransfers=1 \
  -if1=Fedora.iso -of1=/dev/sdb -bs1=4M -size1=2147483648 -oflag1=sync
```

This copies a Fedora ISO to a USB device (`/dev/sdb`) using 4 MB blocks.

---

## License

This project is licensed under the **BSD 3-Clause License**. See the [LICENSE](LICENSE) file for details.

---

## Contributing

Contributions are welcome! To contribute:

1. Fork the repository.
2. Create a new branch for your changes.
3. Submit a pull request with a clear description of your changes.

If you encounter any issues, feel free to open an issue in the repository.

---

## Disclaimer

- Be cautious when using this tool to write to raw devices (e.g., `/dev/sda`, `/dev/nvme0n1`), as it can permanently erase data.
- Ensure you have the necessary permissions to access the specified input/output files or devices.
- We are not responsible for any data loss or hardware damage caused by misuse of this tool.

