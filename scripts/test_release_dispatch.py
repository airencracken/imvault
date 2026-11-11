#!/usr/bin/env python3
"""Contracts for rebuilding immutable release tags with the current workflow."""
from pathlib import Path
import os,re,subprocess,tempfile,textwrap,unittest
ROOT=Path(__file__).resolve().parents[1]
WORKFLOW=ROOT/'.github/workflows/release.yml'


def validation(text):
    start=text.index('          # Validate an explicitly requested existing release tag.')
    end=text.index('          esac',start)+len('          esac\n')
    return textwrap.dedent(text[start:end])


class DispatchTests(unittest.TestCase):
    def setUp(self):
        temporary=tempfile.TemporaryDirectory();self.addCleanup(temporary.cleanup)
        self.repo=Path(temporary.name)
        self.git('init','-q','-b','master')
        (self.repo/'VERSION').write_text('1.2.3\n');self.git('add','VERSION');self.commit('initial')
        (self.repo/'marker').write_text('source');self.git('add','marker');self.commit('release')
        self.git('tag','v1.2.3');self.git('tag','master')
        self.git('update-ref','refs/remotes/origin/master','HEAD')
        self.guard=validation(WORKFLOW.read_text())
    def git(self,*args):
        return subprocess.run(['git',*args],cwd=self.repo,capture_output=True,text=True,check=True).stdout.strip()
    def commit(self,message):self.git('-c','user.name=Fixture','-c','user.email=fixture@example.org','commit','-qm',message)
    def check(self,tag,guard=None):
        return subprocess.run(['bash','-c',guard or self.guard],cwd=self.repo,env=os.environ|{'RELEASE_TAG_INPUT':tag,'RELEASE_REF':'refs/tags/'+tag},capture_output=True,text=True).returncode
    def test_existing_tag_matches_source_version_and_default_branch(self):
        self.assertEqual(self.check('v1.2.3'),0)
        (self.repo/'VERSION').write_text('9.9.9\n');self.assertNotEqual(self.check('v1.2.3'),0)
    def test_missing_non_version_and_adversarial_refs_are_rejected(self):
        for tag in ('master','v1.2.9','refs/heads/master','v1.2.3;false','$(false)','v1.2.3\n','v1.2.3/../master'):
            with self.subTest(tag=tag):self.assertNotEqual(self.check(tag),0)
    def test_different_commit_or_unmerged_tag_cannot_publish(self):
        self.git('update-ref','refs/remotes/origin/master','HEAD^')
        self.assertNotEqual(self.check('v1.2.3'),0)
        before='git merge-base --is-ancestor HEAD origin/master || exit 1'
        self.assertEqual(self.guard.count(before),1)
        self.assertEqual(self.check('v1.2.3',self.guard.replace(before,'true')),0,'reachability boundary fixture must detect removal')
        self.git('update-ref','refs/remotes/origin/master','HEAD')
        (self.repo/'marker').write_text('different');self.git('add','marker');self.commit('different')
        self.assertNotEqual(self.check('v1.2.3'),0)
    def test_release_and_container_jobs_use_the_requested_tag_and_its_revision(self):
        text=WORKFLOW.read_text();containers=(ROOT/'.github/workflows/containers.yml').read_text()
        self.assertIn('ref: ${{ inputs.release_tag || github.ref }}',text)
        self.assertIn('source_ref: ${{ inputs.release_tag || github.ref }}',text)
        self.assertIn('RELEASE_TAG: ${{ inputs.release_tag || github.ref_name }}',text)
        self.assertIn('ref: ${{ inputs.source_ref || github.ref }}',containers)
        self.assertIn('IMAGE_TAG: ${{ inputs.release_tag || github.ref_name }}',containers)
        self.assertEqual(containers.count('IMAGE_REVISION=$(git rev-parse HEAD) || exit 1'),2)
        self.assertIn('needs: [build,',text)
        for path in (ROOT/'.github/workflows').glob('*.yml'):
            source=path.read_text()
            for line in source.splitlines():
                if 'go-version:' in line:self.assertRegex(line,r'''go-version: ['"]1\.26\.11['"]''')

if __name__=='__main__':unittest.main(verbosity=2)
