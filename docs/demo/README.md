# demo assets

`win.gif`, `linux.gif`, `revert.gif`, and `agent.gif` are real recordings, not
mock-ups, structured like short films: each command gets a full-frame title
card - typed out, with a plain-language line under it - then a hard cut to the
guest's actual screen with the command pinned in a bar above the footage. At
any frame, the viewer can tell which command caused what is on screen.

How they are made:

1. Real commands run against a real fork. `terrarium screenshot` captures the
   guest's screen from its video memory, so it needs nothing from the guest.
2. `tools/vmgif` renders the cards, the pinned command bar, a header with the
   machine state and a wall-clock timer, and centered captions, from a
   manifest of captured frames. Reading pauses scale with how much text is on
   screen.
3. `ffmpeg` assembles the frames into the GIF.

The playback is condensed - the shots are moments, not every frame - but
every frame is a real screenshot and the timer reads real elapsed seconds, so
the speed it shows is the speed it was.

`agent.gif` goes further: it is a live Claude session. Claude was asked to win
a game of Minesweeper on a Windows XP fork - an OS with no SSH - and chose
every click itself from the previous screenshot, driving the mouse and
keyboard through the hypervisor. It cleared the board with no losses and no
guessing; the fastest-time dialog at the end is XP's own. The header timer
starts at the first board click and matches Minesweeper's on-screen counter.
