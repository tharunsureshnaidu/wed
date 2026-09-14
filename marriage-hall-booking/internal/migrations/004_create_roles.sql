-- Ported from auth/V4__create_roles_and_user_roles.sql (roles half).
CREATE TABLE roles (
    id SERIAL PRIMARY KEY,
    role_name VARCHAR(50) UNIQUE NOT NULL
);
