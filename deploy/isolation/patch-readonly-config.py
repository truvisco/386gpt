"""Keep explicit /sethome working without granting writes to provider/runtime config.
This patch does not change when the home-channel notice is emitted.
"""
from pathlib import Path
p=Path('/opt/hermes/gateway/config.py')
s=p.read_text()
needle='    from hermes_cli.config import load_config, save_config\n    config = load_config()'
replacement='''    if os.getenv("HERMES_CONFIG_READONLY") == "1":
        import json
        destination = get_hermes_home() / "home-channel.json"
        temporary = destination.with_suffix(".tmp")
        temporary.write_text(json.dumps(home.to_dict()))
        temporary.replace(destination)
        return
    from hermes_cli.config import load_config, save_config
    config = load_config()'''
assert s.count(needle)==1
s=s.replace(needle,replacement)
needle='    _apply_env_overrides(config)\n    _validate_gateway_config(config)'
replacement='''    _apply_env_overrides(config)
    if os.getenv("HERMES_CONFIG_READONLY") == "1":
        import json
        saved_home = get_hermes_home() / "home-channel.json"
        if saved_home.is_file():
            home = HomeChannel.from_dict(json.loads(saved_home.read_text()))
            if home.platform in config.platforms:
                config.platforms[home.platform].home_channel = home
    _validate_gateway_config(config)'''
assert s.count(needle)==1
p.write_text(s.replace(needle,replacement))
compile(p.read_text(),str(p),'exec')
