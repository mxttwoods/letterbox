"""Project checks. Run from the project root: python3 tools/verify.py"""
import subprocess
import sys

out = subprocess.run([sys.executable, "greet.py", "Ada"], capture_output=True, text=True)
assert out.returncode == 0, out.stderr
assert out.stdout.strip() == "Hello, Ada!", out.stdout
print("checks passed")
