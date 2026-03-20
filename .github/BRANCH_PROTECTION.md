# Branch Protection Configuration

This document describes the required branch protection rules for the `main` branch.

## Setup Instructions

Navigate to your GitHub repository settings and configure the following for the `main` branch:

### Required Checks
Require the following status checks to pass before merging:
- `Tests` (from `.github/workflows/test.yml`)
- `Lint` (from `.github/workflows/lint.yml`)
- `Build` (from `.github/workflows/build.yml`)

### Configuration Steps
1. Go to **Settings** → **Branches**
2. Click **Add rule** under Branch protection rules
3. Set Pattern name to `main`
4. Enable the following:
   - ✅ Require a pull request before merging
   - ✅ Require status checks to pass before merging
   - ✅ Require branches to be up to date before merging
   - ✅ Require code reviews before merging (recommended: 1)
   - ✅ Include administrators (to enforce on everyone)

### Selected Status Checks
After enabling "Require status checks to pass before merging", select:
- Tests
- Lint
- Build/ubuntu-latest
- Build/macos-latest
- Build/windows-latest

Once these rules are configured, all pull requests to `main` will require passing tests, linting, and builds before merging.
