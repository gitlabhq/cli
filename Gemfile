source 'https://rubygems.org'

# Exists so the danger-review CI component (2.1.0) skips its auto
# `bundle add gitlab-dangerfiles` path, which currently resolves
# danger-9.6.0 + git-5.0.0 — an incompatible pair that both define
# Git::Base with different types. Remove once danger caps its `git`
# gem dependency upstream.
gem 'gitlab-dangerfiles'
gem 'git', '< 5.0.0'
