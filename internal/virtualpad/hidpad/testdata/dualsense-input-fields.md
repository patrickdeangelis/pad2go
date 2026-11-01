DualSense USB input report: expected descriptor fields, checked by TestDescriptorFieldSummary.
Bit offsets count the report-ID-independent input payload (63 bytes).

| Report | Bit offset | Size | Usage page | Usage(s) | Notes |
|---|---:|---:|---:|---|---|
| 0x01 | 0 | 8 | 0x01 | X 0x30 | logical 0..255 |
| 0x01 | 8 | 8 | 0x01 | Y 0x31 | logical 0..255 |
| 0x01 | 16 | 8 | 0x01 | Z 0x32 | logical 0..255 |
| 0x01 | 24 | 8 | 0x01 | Rz 0x35 | logical 0..255 |
| 0x01 | 32 | 8 | 0x01 | Rx 0x33 | logical 0..255 |
| 0x01 | 40 | 8 | 0x01 | Ry 0x34 | logical 0..255 |
| 0x01 | 48 | 8 | 0xFF00 | 0x20 | vendor byte |
| 0x01 | 56 | 4 | 0x01 | Hat 0x39 | logical 0..7, physical 0..315, unit 0x14 |
| 0x01 | 60 | 15 | 0x09 | buttons 1..15 | logical 0..1 |
| 0x01 | 75 | 13 | 0xFF00 | 0x21 | vendor bits |
| 0x01 | 88 | 416 | 0xFF00 | 0x22 | vendor bytes |
