# Epoch 13 — Game Streaming

- **Tier / path:** `management/hypervisor/`, `modules/applications/`, and
  cabling outside the repository
- **Branch:** none; branches from `main` per piece, label `epoch-13`
- **PR:** #
- **Status:** Not started
- **Closed:**

## Goal

Games run on the estate's hardware, not on the machine in front of the
player. One fixed gaming spot gets the picture over a cable and loses
nothing to it. Every other screen in the house, however underpowered, plays
by streaming. At the end, a laptop does not need to be a gaming machine to
play, and replacing laptops stops being how the household keeps up with
games.

The operator's reason, 2026-10-15: "I'm tired of buying expensive machines
over and over again when I could just have a full server do the compute and
serve it up anywhere no matter what is viewing it."

## Scope

In scope:

- **A machine that runs the games**, with a GPU that has a hardware video
  encoder. Where it runs is the first open decision below.
- **The gaming spot:** HDMI over fibre from that machine to a screen, and a
  way back for the keyboard, mouse and controllers.
- **Streaming to everywhere else:** a streaming host on the gaming machine
  (Sunshine, or Apollo, a fork of it that makes its own virtual display) and
  Moonlight on each screen.
- **The home network's part:** the gaming machine wired, and the access
  points the laptops stream over.
- **Measurement:** the delay each path adds, read from the streaming
  client's own statistics and written down here, so "low latency" is a
  number.

Explicitly out of scope:

- **More than one player at a time.** One GPU runs one game session. Sharing
  a GPU between players (NVIDIA's licensed vGPU, or SR-IOV on some Intel
  cards) is deferred until one player works.
- **Playing away from home.** Reaching the estate from outside is epoch 09's.
- **Buying anything.** This epoch says what the hardware has to be. When and
  whether to buy it is the operator's decision, made separately.

## Decisions

### A cable for the gaming spot, streaming for everywhere else

**Chose:** HDMI over fibre to one fixed spot, and streaming to every other
screen (the operator, 2026-10-15).
**Because:** a cable adds no delay, because it is just a long monitor cable,
but it reaches one place. Streaming reaches any screen on the network, at a
cost measured in milliseconds. Each path covers the other's weakness.
**Rejected:**

- **Streaming at the gaming spot too:** it gives up the one place where no
  added delay is possible.
- **A cable to a laptop:** a laptop's HDMI port is an output, so it cannot
  show what comes in. A USB capture card makes it possible and adds one to
  three frames, which is worse than streaming.
- **HDBaseT** (HDMI and USB over one network cable, to about 100 m): it also
  adds next to no delay, and carries input back on the same run. It was not
  chosen; fibre was. It stays the fallback if the return path for input over
  fibre proves awkward (see Gotchas).

### The stream is encoded, and the bitrate is what is raised

**Chose:** the GPU's hardware encoder. Where there is bandwidth to spare,
raise the bitrate and keep the encoder.
**Because:** uncompressed video does not fit the network:

| Stream           | Uncompressed |
| ---------------- | ------------ |
| 1080p at 60 fps  | ~3 Gbit/s    |
| 1080p at 120 fps | ~6 Gbit/s    |
| 4K at 60 fps     | ~12 Gbit/s   |

Gigabit ethernet carries 1 Gbit/s, and Wi-Fi less. Encoding and decoding
together cost about 2–6 ms, and they are what fits the stream into
30–100 Mbit/s. On a wired network the client runs well above 150 Mbit/s,
which is close to lossless.
**Rejected:** raw video over 10 Gbit ethernet, because laptops do not have
it except through an adapter, and no mainstream streaming software sends
raw.

### Where the delay goes, and what moves it

Before tuning anything, read where the time goes, from the client's
statistics overlay:

| Stage              | Typical                         | What moves it                              |
| ------------------ | ------------------------------- | ------------------------------------------ |
| Capture and encode | 3–8 ms                          | encoder preset, headroom left on the GPU   |
| Network            | under 1 ms wired, 2–15 ms Wi-Fi | cable or wireless, interference, mesh hops |
| Decode             | 1–10 ms                         | the screen's video hardware, the codec     |
| Display            | up to one frame                 | frame rate, vsync                          |

In order of effect: wire the screen where possible; stream at 120 fps (a
frame waits 8 ms instead of 16, even on a 60 Hz screen); match the screen's
own resolution; cap the game slightly below what the GPU can do, so the
encoder never waits; turn vsync off in the game. Expected floor, wired at
120 fps: 8–12 ms added in total.

## Open decisions

Each needs the operator's answer before it is built.

### Where the games run

1. **A Windows machine on the hypervisor, with the GPU handed to it whole.**
   It is one more machine of the estate's, counted in the host's budget and
   built by the converge. The cost is that some games' anti-cheat refuses to
   run on a virtual machine, and a GPU handed over is lost to the host and
   to everything else on it.
2. **A separate gaming box, next to the estate.** Anti-cheat has nothing to
   object to, and nothing the estate runs competes with it. The cost is
   that it is a machine the estate does not build, so how much of it is
   declared in code has to be decided.
3. **Linux in the cluster** (the Games on Whales project). Fully the
   estate's, with the most anti-cheat trouble of the three.

**No recommendation yet.** It turns on two facts nobody has established:
whether the current host has a usable GPU or a free slot for one, and
whether it has the memory. It does not have the memory today: it is at its
budget, and the plan refused a machine on exactly that on 2026-10-13. A
gaming machine wants 6–8 cores and 16 GiB of its own.

### Which games

The anti-cheat question above is decided game by game. The list of games
that have to work is the operator's, and it decides between options 1 and 2.

### What is declared, and what is the floor

The streaming host's settings, the machine's size and the network it sits on
can be code. Windows itself, a game store's sign-in and the client on a
household laptop probably cannot. Each is written down here as code or as
the floor, in the shape of the invariant in `CLAUDE.md`.

### What the gaming machine may reach

It is the household's, not a pull request's, and it is not trusted with the
estate either. Starting from nothing, as everything here does: the screens
that stream from it, the game stores it downloads from, and nothing of the
estate's.

## Acceptance criteria

1. A game started at the gaming spot is shown on its screen over the fibre,
   with keyboard, mouse and controller input back, and no delay a player
   can measure.
2. A wired laptop streams a game at 120 fps, and the client's statistics
   show 12 ms or less added in total. The reading is written here, with
   the game and the settings it was taken under.
3. A laptop on 5 GHz Wi-Fi streams a game playably. Its delay and dropped
   frames are written here beside the wired reading.
4. The gaming machine's memory and cores are in the host's budget, and the
   plan refuses it when it does not fit, as it refuses any machine.
5. The gaming machine reaches only what it is declared to reach, and holds
   no credential of the estate's.
6. Everything about the gaming machine that can be code is code, and what
   is done by hand is written down here as the floor.

## Deferred

- **Two players at once.** Picked up when one player works and a second
  wants to play. It means a second GPU, or GPU sharing.
- **Away from home.** Picked up with epoch 09.

## Gotchas

- **A headless gaming machine has no display to capture.** Apollo makes a
  virtual one. With Sunshine, a dummy HDMI plug does the same job.
- **The cable carries the picture and not the input.** HDMI over fibre is
  video and sound only. Keyboard, mouse and controllers need their own way
  back: USB over fibre, an active USB cable, or a wireless receiver placed
  at the gaming spot within range of the machine. Which one depends on the
  distance, and it is not decided.
- **The cable and the stream show the same session.** One GPU running one
  game shows it on the cable and in the stream at once. It does not give a
  second player a second game.
- **Wi-Fi's average is fine and its spikes are not.** Stutter on a wireless
  laptop is almost always the network, not the encoder. Read the overlay
  before changing a setting.
- **HEVC and AV1 are slower, not faster, on a screen that decodes them in
  software.** Use H.264 on old or weak laptops.
