#!/usr/bin/env python3
"""Run the Postman collection with the OTP step done for you.

Postman cannot read the server log, so the two verify-email requests need an OTP
pasted in by hand - the Java collection says REPLACE_WITH_OTP_FROM_LOGS and
leaves it at that. Locally the log *is* readable, so this registers a customer
and an owner, reads their OTPs, verifies both, and passes the resulting tokens
to newman. The collection then runs top to bottom unattended.

Nothing here is needed when driving the collection by hand in Postman: register,
run `make otp`, paste the code into the `otp` variable, and carry on.
"""
import json
import os
import re
import subprocess
import sys
import time
import urllib.error
import urllib.request

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
BASE = os.environ.get("API_URL", "http://localhost:8080")
LOG = os.path.join(ROOT, "logs", "app.log")


def call(path, body):
    req = urllib.request.Request(BASE + path, method="POST")
    req.add_header("Content-Type", "application/json")
    try:
        return json.load(urllib.request.urlopen(req, json.dumps(body).encode()))
    except urllib.error.HTTPError as e:
        return json.load(e)


def otp_for(email, since):
    """The most recent OTP logged for this address after `since` bytes."""
    for _ in range(20):
        with open(LOG) as fh:
            fh.seek(since)
            found = re.findall(r"target=" + re.escape(email) + r"\s+code=(\d+)", fh.read())
        if found:
            return found[-1]
        time.sleep(0.2)
    return None


def bootstrap(kind, run_id):
    """Register and verify one account; return its tokens and id."""
    email = f"{kind}+{run_id}@example.com"
    phone = "+91" + run_id + {"priya": "0", "rajesh": "1"}.get(kind, "2")
    path = "/api/v1/auth/register" + ("/vendor" if kind == "rajesh" else "")
    mark = os.path.getsize(LOG) if os.path.exists(LOG) else 0

    r = call(path, {"fullName": kind.title(), "email": email,
                    "phoneNumber": phone, "password": "SecurePass@123"})
    if not r.get("success"):
        sys.exit(f"register {kind} failed: {r.get('message')}")

    code = otp_for(email, mark)
    if not code:
        sys.exit(f"no OTP logged for {email} - is LOG_OTP_CODES=true in .env?")

    v = call("/api/v1/auth/register/verify-email", {"target": email, "otpCode": code})
    if not v.get("success"):
        sys.exit(f"verify {kind} failed: {v.get('message')}")
    d = v["data"]
    return {"email": email, "phone": phone, "access": d["accessToken"],
            "refresh": d["refreshToken"], "id": d["user"]["id"]}


def grant_admin(user_id):
    """Promote an account to ROLE_ADMIN.

    There is no API for this by design - an endpoint that hands out admin would
    be a hole - so the run does it directly in Postgres, the same way a real
    deployment seeds its first admin.
    """
    db = os.environ.get(
        "TEST_DATABASE_URL",
        "postgres://postgres:postgres@localhost:5432/venue?sslmode=disable")
    sql = (f"INSERT INTO user_roles (user_id, role_id) "
           f"SELECT {int(user_id)}, id FROM roles WHERE role_name = 'ROLE_ADMIN' "
           f"ON CONFLICT DO NOTHING")
    subprocess.run(["psql", db, "-tAc", sql], capture_output=True)


def main():
    run_id = str(int(time.time()))[-9:]
    customer = bootstrap("priya", run_id)
    owner = bootstrap("rajesh", run_id)
    admin = bootstrap("admin", run_id)
    grant_admin(admin["id"])
    # The token was minted before the role existed, so mint a fresh one.
    a = call("/api/v1/auth/login", {"identifier": admin["email"],
                                    "password": "SecurePass@123"})
    if not a.get("success"):
        sys.exit(f"admin login failed: {a.get('message')}")
    admin["access"] = a["data"]["accessToken"]

    # The owner needs a vendor business before they may list a facility.
    req = urllib.request.Request(BASE + "/api/v1/vendors/me", method="PUT")
    req.add_header("Content-Type", "application/json")
    req.add_header("Authorization", "Bearer " + owner["access"])
    try:
        urllib.request.urlopen(req, json.dumps(
            {"businessName": "Palace Events", "businessAddress": "42 MG Road",
             "businessPhone": owner["phone"], "businessEmail": owner["email"]}).encode())
    except urllib.error.HTTPError as e:
        sys.exit(f"vendor setup failed: {e.read()[:200]}")

    env = {
        "baseUrl": BASE, "runId": run_id,
        "customerEmail": customer["email"], "customerPhone": customer["phone"],
        "ownerEmail": owner["email"], "ownerPhone": owner["phone"],
        "accessToken": customer["access"], "refreshToken": customer["refresh"],
        "userId": str(customer["id"]),
        "ownerToken": owner["access"],
        "adminToken": admin["access"],
    }
    cmd = ["newman", "run", os.path.join(ROOT, "postman_collection.json")]
    for k, v in env.items():
        cmd += ["--env-var", f"{k}={v}"]
    cmd += sys.argv[1:]
    print(f"bootstrapped {customer['email']} / {owner['email']}\n")
    sys.exit(subprocess.call(cmd))


if __name__ == "__main__":
    main()
