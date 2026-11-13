import sys


def load_env(path=".env"):
    values = {}
    with open(path) as f:
        for line in f:
            if "=" in line:
                key, value = line.strip().split("=", 1)
                values[key] = value
    return values


token = load_env().get("REPORT_TOKEN")
if not token:
    sys.exit("REPORT_TOKEN is not set in .env")
print(f"Report ready (authenticated with a {len(token)}-character token).")
