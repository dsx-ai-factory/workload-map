# SPDX-License-Identifier: Apache-2.0
# Copyright (c) 2026 NVIDIA Corporation

from ray import serve


@serve.deployment
class Ready:
    def __call__(self, request):
        return "karta-rayservice-ready"


app = Ready.bind()
