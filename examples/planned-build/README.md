# planned-build

Commands recorded rather than run, read before they are sent, then sent
together.

Two things make the indirection worth it. A step can name what an earlier step
is *going* to create, so a build is written in one pass instead of stopping at
each split to learn a pane ID. And commands needing no answer travel in one tmux
command list, so the window is built in fewer invocations than it has
operations.

## Running it

```console
$ go -C examples run ./planned-build
```

On a tmux server with nothing else on it, it prints:

<!-- docs:output examples/planned-build/output.txt -->

```text
step 0: tmux select-layout -t @1 tiled
step 1: tmux split-window -t @1 -v -P -F#{pane_id}
step 2: rendered when the split has reported its pane
step 3: rendered when the split has reported its pane
step 4: rendered when the split has reported its pane
step 5: rendered when the split has reported its pane
tmux invocation carrying steps [0], ends because it alone
tmux invocation carrying steps [1], ends because it creates
tmux invocation carrying steps [2 3 4], ends because it chained
tmux invocation carrying steps [5], ends because it captures
operations: 6, tmux invocations: 4
step 0 select-layout    complete created "" stdout []
step 1 split-window     complete created "%2" stdout []
step 2 select-pane      complete created "" stdout []
step 3 send-keys        complete created "" stdout []
step 4 send-keys        complete created "" stdout []
step 5 display-message  complete created "" stdout ["editor"]
```

<!-- docs:end -->

## What to look at

**A pane named before it exists.** The split has not run, so the pane it returns
is not real yet — but it can already be named, which is what lets every step
after it be written here rather than after a round trip.

**Preview renders what will be sent.** A step naming a pane no earlier step
created here comes back nil, and that is not an error: it is the plan saying
this one renders once the split reports. Anything it genuinely cannot render is
an error, which is the point of reading a plan before sending it.

**Explain says why each group ends.** Two things end one early: a command whose
new object's ID a later step needs, and a command whose output is read. tmux
answers a command list with one merged stdout, so neither can share it.

**More results than calls.** `SendKeys` with a command records the keys and the
Enter that submits them, because that is the two tmux commands it sends.

## Testing your own version

```console
$ go -C examples test ./planned-build
```

The example returns an error rather than printing a wrong answer if the plan
stops grouping, if the split reports no pane, or if the last step does not read
back the title an earlier step set — so those checks bite without the test
repeating them.
