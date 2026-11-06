CREATE EXTENSION IF NOT EXISTS pgcrypto;

CREATE TABLE student_groups (
    group_uuid UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    group_name VARCHAR(20) NOT NULL UNIQUE,
    group_prefix VARCHAR(10) NOT NULL,
    group_number INT NOT NULL,
    CONSTRAINT uq_student_groups_prefix_number UNIQUE (group_prefix, group_number)
);

CREATE TABLE teachers (
    teacher_uuid UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    last_name VARCHAR(30) NOT NULL,
    first_name VARCHAR(20) NOT NULL,
    middle_name VARCHAR(30),
    login VARCHAR(30) NOT NULL UNIQUE,
    password_hash TEXT NOT NULL
);

CREATE TABLE students (
    student_uuid UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    last_name VARCHAR(30) NOT NULL,
    first_name VARCHAR(20) NOT NULL,
    middle_name VARCHAR(30),
    login VARCHAR(50) NOT NULL UNIQUE,
    password_hash TEXT NOT NULL,
    group_uuid UUID NOT NULL,
    CONSTRAINT fk_students_group
        FOREIGN KEY (group_uuid)
        REFERENCES student_groups(group_uuid)
);

CREATE TABLE courses (
    course_uuid UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    course_name VARCHAR(60) NOT NULL,
    teacher_uuid UUID NOT NULL,
    created_at TIMESTAMP NOT NULL DEFAULT NOW(),
    CONSTRAINT fk_courses_teacher
        FOREIGN KEY (teacher_uuid)
        REFERENCES teachers(teacher_uuid)
);

CREATE TABLE course_groups (
    course_uuid UUID NOT NULL,
    group_uuid UUID NOT NULL,
    created_at TIMESTAMP NOT NULL DEFAULT NOW(),
    PRIMARY KEY (course_uuid, group_uuid),
    CONSTRAINT fk_course_groups_course
        FOREIGN KEY (course_uuid)
        REFERENCES courses(course_uuid)
        ON DELETE CASCADE,
    CONSTRAINT fk_course_groups_group
        FOREIGN KEY (group_uuid)
        REFERENCES student_groups(group_uuid)
        ON DELETE CASCADE
);

CREATE TABLE tasks (
    task_uuid UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    course_uuid UUID NOT NULL,
    task_mode VARCHAR(8) NOT NULL DEFAULT 'expected',
    task_title VARCHAR(80) NOT NULL,
    task_text VARCHAR NOT NULL,
    result TEXT NOT NULL DEFAULT '',
    allowed_lang TEXT NOT NULL DEFAULT '["c","cpp","go"]',
    time_limit_ms INT CHECK (time_limit_ms >= 0),
    memory_limit_kb INT CHECK (memory_limit_kb >= 0),
    created_at TIMESTAMP NOT NULL DEFAULT NOW(),
    CONSTRAINT fk_tasks_course
        FOREIGN KEY (course_uuid)
        REFERENCES courses(course_uuid),
    CONSTRAINT chk_task_mode
        CHECK (task_mode IN ('tests', 'expected', 'none'))
);

CREATE TABLE task_tests (
    test_uuid UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    task_uuid UUID NOT NULL,
    test_index SMALLINT NOT NULL,
    visible_to_student BOOLEAN NOT NULL DEFAULT TRUE,
    input_path TEXT NOT NULL,
    output_path TEXT NOT NULL,
    created_at TIMESTAMP NOT NULL DEFAULT NOW(),
    CONSTRAINT fk_task_tests
        FOREIGN KEY (task_uuid)
        REFERENCES tasks(task_uuid),
    CONSTRAINT uq_task_tests_index
        UNIQUE (task_uuid, test_index)
);

CREATE TABLE submissions (
    submission_uuid UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    course_uuid UUID NOT NULL,
    task_uuid UUID NOT NULL,
    student_uuid UUID NOT NULL,
    lang VARCHAR(10) NOT NULL,
    lang_version VARCHAR(10) NOT NULL,
    source_path TEXT NOT NULL,
    source_hash VARCHAR NOT NULL,
    tests_hash VARCHAR NOT NULL DEFAULT '',
    checker_output TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMP NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP NOT NULL DEFAULT NOW(),
    CONSTRAINT fk_submissions_task
        FOREIGN KEY (task_uuid)
        REFERENCES tasks(task_uuid),
    CONSTRAINT fk_submissions_course
        FOREIGN KEY (course_uuid)
        REFERENCES courses(course_uuid),
    CONSTRAINT fk_submissions_student
        FOREIGN KEY (student_uuid)
        REFERENCES students(student_uuid),
    CONSTRAINT uq_submissions_task_student_lang_version
        UNIQUE (task_uuid, student_uuid, lang, lang_version)
);

CREATE INDEX idx_courses_teacher_uuid ON courses(teacher_uuid);
CREATE INDEX idx_student_groups_prefix_number ON student_groups(group_prefix, group_number);
CREATE INDEX idx_course_groups_course_uuid ON course_groups(course_uuid);
CREATE INDEX idx_course_groups_group_uuid ON course_groups(group_uuid);
CREATE INDEX idx_tasks_course_uuid ON tasks(course_uuid);
CREATE INDEX idx_task_tests_task_uuid ON task_tests(task_uuid);
CREATE INDEX idx_submissions_course_uuid ON submissions(course_uuid);
CREATE INDEX idx_submissions_source_hash ON submissions(source_hash);
CREATE INDEX idx_submissions_tests_hash ON submissions(tests_hash);
CREATE TABLE submission_attempts (
    attempt_uuid UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    submission_uuid UUID NOT NULL,
    course_uuid UUID NOT NULL,
    task_uuid UUID NOT NULL,
    student_uuid UUID NOT NULL,
    lang VARCHAR(10) NOT NULL,
    lang_version VARCHAR(10) NOT NULL,
    source_path TEXT NOT NULL,
    source_hash VARCHAR NOT NULL,
    tests_hash VARCHAR NOT NULL DEFAULT '',
    checker_output TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMP NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP NOT NULL DEFAULT NOW(),
    CONSTRAINT fk_submission_attempts_submission
        FOREIGN KEY (submission_uuid)
        REFERENCES submissions(submission_uuid)
        ON DELETE CASCADE,
    CONSTRAINT fk_submission_attempts_task
        FOREIGN KEY (task_uuid)
        REFERENCES tasks(task_uuid)
        ON DELETE CASCADE,
    CONSTRAINT fk_submission_attempts_course
        FOREIGN KEY (course_uuid)
        REFERENCES courses(course_uuid)
        ON DELETE CASCADE,
    CONSTRAINT fk_submission_attempts_student
        FOREIGN KEY (student_uuid)
        REFERENCES students(student_uuid)
        ON DELETE CASCADE
);

CREATE INDEX idx_submission_attempts_course_uuid ON submission_attempts(course_uuid);
CREATE INDEX idx_submission_attempts_task_uuid ON submission_attempts(task_uuid);
CREATE INDEX idx_submission_attempts_student_uuid ON submission_attempts(student_uuid);
CREATE INDEX idx_submission_attempts_submission_uuid ON submission_attempts(submission_uuid);