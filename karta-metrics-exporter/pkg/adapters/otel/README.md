<!-- SPDX-License-Identifier: Apache-2.0 -->
<!-- Copyright (c) 2026 NVIDIA Corporation -->

# OTel Collector

`Collector.Register` takes an OTel Meter and a Store `Capturer` and returns a
`metric.Registration`. Collector publishes all supported workload phases.
The generated Collector mock lets startup be tested independently.

The implementation will register one callback for the four KEP metric families.
Each collection captures Store once, checks evidence validity and emits saved
values. It does not read Kubernetes or run expressions.

`app.Run` calls `registration.Unregister()` after `Runtime.Run` returns. This
removes the callback from the meter. Any unregister error is returned alongside
the runtime error. The reader, HTTP endpoint and provider shutdown are not
implemented.

No instruments or collection callback are implemented here. The bootstrap package uses
a no-op meter and registers no instruments.
