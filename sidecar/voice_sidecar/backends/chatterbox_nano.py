"""Chatterbox Nano (Resemble AI, MIT): Chatterbox Turbo's architecture at
110M parameters, for the processor. English, with the same paralinguistic
tags ([laugh], [sigh], [cough], ...) and cloning from a clip longer than 5
seconds. Resemble quotes about 3 times faster than speech on 8 CPU cores.

It runs on the processor whatever the PC has (its environment gets the CPU
PyTorch), so it never takes graphics memory from the GPU voices.

Options: device=cpu|cuda (cpu unless set), threads=N (PyTorch's own choice,
the physical cores, unless set). Per request, extra may override
temperature, top_p, top_k and repetition_penalty, as for Turbo.
"""

from . import chatterbox_turbo


class Backend(chatterbox_turbo.Backend):
    nano = True
    label = "Chatterbox Nano"

    def device(self, options):
        if options.get("threads"):
            import torch

            torch.set_num_threads(int(options["threads"]))
        return options.get("device") or "cpu"
