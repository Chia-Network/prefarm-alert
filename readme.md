# Prefarm Alert

Alerts when any new activity is detected on a custody singleton.

## Observer Info

In order to configure a deployment, you need base64 encoded observer info. From your internal-custody tool, run the following commands:

* `cic export_config -p -f "observer-info.txt"`
* `cat observer-info.txt| base64`

The result of the last command will be base64 encoded observer info, which can be used directly as the helm value `observerDataB64` (via vault).
