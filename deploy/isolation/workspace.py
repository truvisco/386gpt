"""Migrate one account's Docker workspace volume to an explicit host directory.
The old volume is retained. Never mount the account configuration directory.
"""
import argparse
import json
from pathlib import Path
import re
import shutil
import subprocess
import tempfile

PROJECT = re.compile(r'386gpt-(?:isolated|account-[a-f0-9]{64}|test-[a-z0-9-]+)')

def ensure_workspace(project, environment, destination, runner=subprocess.run):
    if not PROJECT.fullmatch(project):
        raise ValueError('Unexpected workspace project')
    environment, destination = Path(environment), Path(destination)
    if not destination.is_absolute() or destination.is_symlink():
        raise ValueError('Workspace must be a dedicated absolute non-symlink directory')
    text = environment.read_text()
    configured = [line.split('=',1)[1] for line in text.splitlines() if line.startswith('HERMES_WORKSPACE=')]
    if configured:
        if len(configured)!=1 or configured[0]!=str(destination) or not destination.is_dir():
            raise RuntimeError('Configured workspace is missing or mismatched; refusing an empty replacement')
        return
    if destination.exists():
        raise RuntimeError('Unregistered workspace exists; refusing to overwrite it')
    if not re.fullmatch(r'[/A-Za-z0-9_.-]+',str(destination)):
        raise ValueError('Unsupported workspace path')

    def run(args, **options):
        return runner(args,capture_output=True,text=True,timeout=300,check=False,**options)
    container=project+'-hermes-1'
    result=run(['docker','container','inspect',container])
    running=False
    if result.returncode and not any(message in result.stderr.lower() for message in ('no such container','no such object')):
        raise RuntimeError('Cannot inspect existing workspace container')
    if result.returncode==0:
        data=json.loads(result.stdout)[0]
        mounts=[m for m in data['Mounts'] if m['Destination']=='/workspace']
        if len(mounts)!=1 or mounts[0]['Type']!='volume' or mounts[0]['Name']!=project+'_workspace':
            raise RuntimeError('Unexpected existing workspace mount')
        running=data['State']['Running']
    volume=project+'_workspace'
    result=run(['docker','volume','inspect',volume])
    if result.returncode and 'no such volume' not in result.stderr.lower():
        raise RuntimeError('Cannot inspect existing workspace volume')
    source=result.returncode==0
    if source:
        metadata=json.loads(result.stdout)[0]
        if (metadata.get('Labels') or {}).get('com.docker.compose.project')!=project:
            raise RuntimeError('Workspace volume belongs to a different project')
    destination.parent.mkdir(mode=0o700,parents=True,exist_ok=True)
    staging=Path(tempfile.mkdtemp(prefix='.'+destination.name+'-migration-',dir=destination.parent))
    args=['docker','run','--rm','--network','none','--user','0:0','--cap-drop','ALL',
          '--cap-add','CHOWN','--cap-add','DAC_OVERRIDE','--cap-add','FOWNER',
          '--security-opt','no-new-privileges:true','--entrypoint','/bin/sh',
          '--mount','type=bind,src='+str(staging)+',dst=/target']
    if source:args+=['--mount','type=volume,src='+volume+',dst=/source,readonly']
    args+=['386gpt-hermes:isolated','-ec',('cp -a /source/. /target/; ' if source else '')+'chown 10001:10001 /target; chmod 0750 /target']
    if running and run(['docker','stop',container]).returncode:
        shutil.rmtree(staging,ignore_errors=True)
        raise RuntimeError('Could not quiesce the workspace for migration')
    try:
        if run(args).returncode:raise RuntimeError('Workspace migration failed; original volume retained')
        staging.rename(destination)
        temporary=environment.with_suffix('.workspace.tmp')
        temporary.write_text(text.rstrip()+'\nHERMES_WORKSPACE='+str(destination)+'\n')
        temporary.chmod(0o600)
        temporary.replace(environment)
    except Exception:
        if running:run(['docker','start',container])
        shutil.rmtree(staging,ignore_errors=True)
        raise

if __name__=='__main__':
    p=argparse.ArgumentParser()
    p.add_argument('--project',required=True)
    p.add_argument('--environment',type=Path,required=True)
    p.add_argument('--workspace',type=Path,required=True)
    a=p.parse_args()
    ensure_workspace(a.project,a.environment,a.workspace)
    print('Workspace host directory ready; original volume retained if present.')
