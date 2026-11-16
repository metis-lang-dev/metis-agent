# Under the readings: the world and the plant

What a behaviour author must know about the simulator, the base and the shields beneath the classes.

Part of the metis pattern catalog (`patterns/README.md`): the template, the other categories, the anti-pattern index. Each pattern names its source so you can open it; `Loop:` says how the pi agent recognizes and applies it. Credits carry the author; the text speaks of the runs, never of 'me'.

### Check the world first
*(from george)*

Intent: before reading a run's behaviour, prove the world it ran in was the world you meant.
Problem: a run looks like a policy or tuning fault and the world itself was broken. In step 4 `people` was empty on 240 of 240 ticks, or the chair "arrived" in 33 ticks in a scene that should have had three people in it. A clean card on an empty world reads as a success.
Shape: one fact written at the start of each run, read before anything else: `agents-at-start.txt` ("agents 3 [...]"). If it is wrong the run is void, and is not read for behaviour.
Example: metis-wheelchair `tools/sim/count_agents.py`; run s4-door: arrived tick 33, `people` 0 of 240, `Failed to call service get_agents` in `launch.log`. The user caught it by looking at the screen (the NPCs were at the origin, half under the floor); the runner's logs had not flagged it.
Consequences: one more file per run. Void runs get discarded, so counts need saying "of N that started".
Related: Sim race in the log. Anti-pattern: the card on an empty world.
Loop: evidence: a run far better or far worse than its neighbours, an empty list where the scene names bodies. Verb: read the start fact before blame. A surprise from a void run is not proposed as a case.

### Sim race in the log
*(from george)*

Intent: tell a startup race from a behaviour fault, across runs of the same command.
Problem: the same command gives two different worlds. The symptom looks like the rules (brake for ever, a chair that never leaves). The tell is that it comes and goes with no change to anything.
Shape: compare one good and one bad launch log for the same line. Look for a short timeout on a service in bringup. Count bad over started, and say the count. After a fix, count again: two clean starts of a race that hit 2 in 5 do not prove it gone.
Example: `hunav_gazebo_wrapper/src/HuNavPlugin.cpp` `InitializeAgents`: `result.wait_for(200ms)`, else `Failed to call service get_agents`, and nothing retries. 2 of 5 step 4 runs. Not in any of the 23 earlier runs counted; why not is unknown. Also of this family: `ros2 lifecycle set` failing silently and leaving the collision monitor inactive, so `/cmd_vel` has no publisher and the robot never moves (bringup rule: loop until `active`, write the state). `ros2 param dump` hanging a runner for an hour.
Consequences: bringup gets a check and a state file per dependency. A fix in a vendored sim is a local patch to carry (`install-hunav.sh`).
Related: Check the world first. Anti-pattern: tuning a rule against a race; retrying until it passes.
Loop: evidence: same command and pins, different fired rows at the same tick. Verb: `python -m metis.trace diff A B` prints the first tick where two runs' fired rows differ, before blame. Refuse to mint a case from it until the world's start fact agrees across runs.

### Measure the plant
*(from george)*

Intent: learn what the base does with a command, open loop, before tuning anything or reasoning about rules above it.
Problem: `Failed to make progress`, stalls, aborts 2 m short. Every theory is about planners or parameters, and the parameter file describes a robot that does not exist.
Shape: steps on the command topic with no planner: linear, reverse, turn in place, both; read ground truth, not the commanded value; write a small table. Keep the table as the plant model and give a bench the same numbers.
Example: `tools/sim/plant_test.py`; `docs/MOTION.md` §7: about 0.8 x commanded speed; no reverse beyond -0.15 m/s; 0.5 m/s2; yaw at 0.05 rad/s none, at 0.158 about 0.01, at 0.5 0.24. The stock file assumed 2.5 m/s2 and 3.2 rad/s2. FollowPath went 0 of 8 to 8 of 8 on the tracker. Why this base has the dead zone is not established (the URDF's wheel friction is likely).
Consequences: a cap is an upper bound, not a setpoint, so what the policy commands is not what the chair does: `ros_base.setpoint` inverts the table. The numbers belong to this base in this simulator and must be remeasured for the chair.
Related: Ramp out of the dead zone. Anti-pattern: tuning against the error message.
Loop: evidence: realised motion disagrees with the command in a trace. A rule author should read realised readings (`moving(Z)`, `blocked(Z)`, and the record's `moved`), and never treat a commanded speed as a fact about the chair.

### Ramp out of the dead zone
*(from george)*

Intent: never let a controller approach a command from below when the base does not answer below a threshold.
Problem: a robot asked to go that does nothing, with no error, until a progress checker aborts. Commands decay or start inside the zone: anything that plans from measured velocity gets a stuck robot from a stuck robot.
Shape: find the small repeated value in the trace and explain it. Here 0.05 rad/s (RPP: measured rate + `max_angular_accel` x 0.05 s) and 0.16 (DWB: 3.2 x 0.05 s). Let one stage alone hold the acceleration limit, downstream, open loop.
Example: `tools/sim/make-params.py`: `max_angular_accel` 20, with the smoother at 1.05 rad/s2. In the stuck leg the wheels turned 0.000 rad; the pose did not cause it. A trace of a stall: `steps/followup/evidence/stall.csv`.
Consequences: the limit in the controller's file is no longer the base's; the comment must say why. The same deadlock takes other forms: a behaviour that holds until motion starts, while motion waits on the behaviour.
Related: Measure the plant, Shield on the motion. Anti-pattern: a timer that kicks the robot.
Loop: evidence: `blocked(Z)` on with a non-zero command at a constant small value. Say which layer produced that exact number before proposing a rule about being blocked.

### Shield on the motion
*(from george)*

Intent: a floor below the policy must judge what is commanded, or it will make deadlocks it cannot leave. **Open: stated as the problem; the first fix tried, a smaller region, did not work.**
Problem: a fixed stop region forbids every motion while anything is in it, the turn away included. Something static in it holds the robot for good, and the policy sees only `blocked`. Same shape as both-standing on the road: a danger true for ever, needing a bounded motion away.
Shape, what stands: a projected test (`FootprintApproach`, footprint along the commanded velocity, 1.6 s) carrying the speed margin; the reading is the monitor's input asking and its output zero for 0.2 s, over linear and turn. Shrinking the stop region is withdrawn by the architect. Backing out was withdrawn too: with no rear sensor a blind reverse is itself a shield violation. Still open: a velocity-gated polygon (Jazzy's VelocityPolygon).
Example: `tools/sim/collision_monitor.yaml`. Worth having: approach took the head-on leg from 51 stops to 6 and from 18-22 s to about 14.5 s. **Not enough:** the same line at a table with the stop region at 0.75 m and at 0.375 m stuck in both (floor 171 and 169 of 240, no turn output in either). The robot only stops closer. The test line aimed at the table, which is the worst case; a plan would not. Humble's stop polygon exempts nothing. Not yet tested either way.
Consequences: the floor does not protect a stopped chair: a non-reacting person reached 0.54 m, and the laser is blind behind. Self-hits must be filtered before a swept test (`scan_self_filter.py`). A floor stop is hardware's or policy's to resolve, and today nothing resolves it.
Related: both-standing, the standing resolution (pascal's). Anti-patterns: the timer in disguise; a smaller radius as the cure.
Loop: evidence: `floor_stopped` held across many ticks with a free route elsewhere. A rule can wait or drive away, but must not assume the floor will let it.

