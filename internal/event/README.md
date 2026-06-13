The code in `event` was originally a part of https://github.com/kelindar/event (v1.5.2)

The original code uses a `time.Ticker` to process the event queue which caused a large increase in CPU usage ([#189](https://github.com/mostlygeek/llama-swap/issues/189)). This code was ported to remove the ticker and instead be more event driven.
