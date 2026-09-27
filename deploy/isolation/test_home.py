"""Container-only regression: explicit home selection persists with immutable config."""
import os
from pathlib import Path
import tempfile
with tempfile.TemporaryDirectory() as directory:
    os.environ['HERMES_HOME']=directory
    os.environ['HERMES_CONFIG_READONLY']='1'
    os.environ['API_SERVER_ENABLED']='false'
    config=Path(directory)/'config.yaml'
    original='platforms:\n  telegram:\n    enabled: false\n'
    config.write_text(original)
    config.chmod(0o400)
    from gateway.config import HomeChannel, Platform, persist_home_channel, load_gateway_config
    persist_home_channel(HomeChannel(platform=Platform.TELEGRAM,chat_id='fixture',name='Fixture'))
    assert config.read_text()==original
    assert load_gateway_config().get_home_channel(Platform.TELEGRAM).chat_id=='fixture'
    print('PASS: explicit home selection persists without editing runtime config or suppressing notices.')
