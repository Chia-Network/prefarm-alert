# Prefarm Alert

Alerts when any new activity is detected on the prefarm.

```text
Okay I added a cic audit command that exports a JSON list of all the actions that have happened to the singleton as you have it synced.  If you want to test this out (@devops):

1) Follow the install instructions on the repo README https://github.com/Chia-Network/internal-custody

2) Copy/Paste the "Observer Info.txt" file that I'm about to attach into the working directory.

3) Run cic sync -c "Observer Info.txt" (will be attached after)

4) Run cic audit -f audit.log

It's probably enough to just run this once a day or once a week and if the size of the list ever changes just send the new elements straight up as an alert.  They should contain all of the necessary info to see what is going on.
```
