# karta
Copyright (c) 2026 NVIDIA Corporation

This product includes software developed by NVIDIA Corporation.

This product includes the following third-party software components:

For a complete list of third-party software licenses, please see the
THIRD_PARTY_LICENSES file.

Third-party Dependencies:
{{ $prev := "" }}{{ range .Direct }}{{ if ne .Name "github.com/run-ai/karta" }}{{ $key := printf "%s (%s)" .Name .LicenceType }}{{ if ne $key $prev }}- {{ $key }}
{{ end }}{{ $prev = $key }}{{ end }}{{ end }}

Full license texts for these dependencies are available in the THIRD_PARTY_LICENSES file.
