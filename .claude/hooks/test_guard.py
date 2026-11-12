#!/usr/bin/env python3
import importlib.util
from pathlib import Path
import unittest

path = Path(__file__).with_name('guard.py')
spec = importlib.util.spec_from_file_location('guard', path)
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)

class GuardTests(unittest.TestCase):
    def test_read_only(self):
        for cmd in ['go test ./...', 'terraform validate', 'git status --short', 'aws sts get-caller-identity']:
            self.assertIsNone(module.check({'tool_name':'Bash','tool_input':{'command':cmd}}))
    def test_mutations(self):
        for cmd in ['terraform apply', 'terraform destroy', 'aws ec2 run-instances --image-id foo', 'kubectl delete pod test', 'git reset --hard HEAD', 'rm -rf /tmp/a']:
            self.assertIsNotNone(module.check({'tool_name':'Bash','tool_input':{'command':cmd}}), cmd)
    def test_secrets(self):
        for filename in ['/tmp/.env', '/tmp/id_rsa', '/tmp/foo.tfstate', '/Users/me/.aws/credentials']:
            self.assertIsNotNone(module.check({'tool_name':'Write','tool_input':{'file_path':filename}}))

if __name__ == '__main__': unittest.main()
