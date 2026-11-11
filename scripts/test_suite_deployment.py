#!/usr/bin/env python3
"""Exercise optional suite settings through the actual service launch formats."""
from pathlib import Path
import json,os,re,shlex,shutil,subprocess,sys,tempfile,unittest,uuid
ROOT=Path(__file__).resolve().parents[1]
APP=re.search(r'^module\s+(\S+)',(ROOT/'go.mod').read_text(),re.M)[1].rsplit('/',1)[-1]
PREFIX=APP.upper()+'_'
SUFFIXES=('APPS','SHARED_ISSUER','SHARED_CLIENT_ID','SHARED_CLIENT_SECRET')
VALUES=dict(zip((PREFIX+x for x in SUFFIXES),('[{"id":"songstead","url":"https://music.example.org"}]','https://identity.example.org','fixture-client','literal $value `text` "quotes" ; $(false)')))
DIRECTORY='deploy' if APP in ('folkweave','tidings') else 'contrib'


def inherited(source):
    with tempfile.TemporaryDirectory() as temporary:
        init=Path(temporary)/'service';init.write_text(source)
        assignments='\n'.join(key+'='+shlex.quote(value) for key,value in VALUES.items())
        probe='import json,os; print(json.dumps({key:os.environ.get(key) for key in '+repr(list(VALUES))+'}))'
        script=assignments+'\n. '+shlex.quote(str(init))+'\n'+shlex.join([sys.executable,'-c',probe])
        env={k:v for k,v in os.environ.items() if not k.startswith(PREFIX)}
        result=subprocess.run(['/bin/sh','-c',script],env=env,capture_output=True,text=True,timeout=10,check=True)
        return json.loads(result.stdout)


class SuiteDeploymentTests(unittest.TestCase):
    def test_plain_openrc_settings_reach_the_child_as_literal_values(self):
        source=(ROOT/DIRECTORY/'openrc'/APP).read_text()
        self.assertEqual(inherited(source),VALUES)

    def test_removing_the_suite_exports_is_detected(self):
        source=(ROOT/DIRECTORY/'openrc'/APP).read_text()
        lines=[line for line in source.splitlines() if line.startswith('export ') and PREFIX+'SHARED_ISSUER' in line]
        self.assertEqual(len(lines),1)
        self.assertNotEqual(inherited(source.replace(lines[0],'')),VALUES,'removed-export mutation survived')

    def test_native_templates_leave_all_integrations_disabled(self):
        conf=APP+('.conf' if DIRECTORY=='deploy' else '.confd')
        for path in (ROOT/DIRECTORY/'openrc'/conf,ROOT/DIRECTORY/'systemd'/(APP+'.env')):
            for key in VALUES:
                self.assertEqual(len(re.findall(r'^# '+key+r'=""$',path.read_text(),re.M)),1,(path,key))

    @unittest.skipUnless(shutil.which('docker'),'Docker Compose is required')
    def test_compose_forwards_literal_values_and_defaults_to_disabled(self):
        env={k:v for k,v in os.environ.items() if not k.startswith(PREFIX) and k not in ('COMPOSE_FILE','COMPOSE_PROFILES')}
        for values in ({},VALUES):
            result=subprocess.run(['docker','compose','-f',str(ROOT/'docker-compose.yml'),'config','--format','json'],env=env|values,capture_output=True,text=True,check=True,timeout=20)
            actual=json.loads(result.stdout)['services'][APP]['environment']
            self.assertEqual({key:actual.get(key) for key in VALUES},{key:value.replace('$','$$') for key,value in values.items()} if values else {key:'' for key in VALUES})

    @unittest.skipUnless(os.environ.get('COMFYWARE_CONTAINER_ENV_TEST')=='1','requires the isolated container integration check')
    def test_actual_compose_container_receives_unmodified_values(self):
        image=re.search(r'^FROM (alpine:\S+)',(ROOT/'Dockerfile.release').read_text(),re.M)[1]
        env={k:v for k,v in os.environ.items() if not k.startswith(PREFIX) and k not in ('COMPOSE_FILE','COMPOSE_PROFILES')}|VALUES
        with tempfile.TemporaryDirectory() as temporary:
            override=Path(temporary)/'override.yml'
            override.write_text('services:\n  '+APP+':\n    image: '+image+'\n')
            command=['docker','compose','--project-name','suite-env-'+uuid.uuid4().hex,
                     '-f',str(ROOT/'docker-compose.yml'),'-f',str(override)]
            try:
                result=subprocess.run(command+['run','--rm','-T','--no-deps','--entrypoint','/usr/bin/env',APP],env=env,capture_output=True,text=True,check=True,timeout=120)
                actual=dict(line.split('=',1) for line in result.stdout.splitlines() if '=' in line)
                self.assertEqual({key:actual.get(key) for key in VALUES},VALUES)
            finally:
                subprocess.run(command+['down','--volumes','--remove-orphans'],env=env,capture_output=True,check=True,timeout=60)

if __name__=='__main__':unittest.main(verbosity=2)
